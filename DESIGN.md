# PriorityServe — Design Document

> **Purpose of this doc:** Detailed enough to hand to an AI coding assistant for implementation, section by section. Each phase is self-contained. Read top-to-bottom once before coding anything.

---

## Table of Contents

1. [What This Is](#1-what-this-is)
2. [Goals and Non-Goals](#2-goals-and-non-goals)
3. [How It Works — Big Picture](#3-how-it-works--big-picture)
4. [Repository Layout](#4-repository-layout)
5. [Configuration](#5-configuration)
6. [Core Data Types](#6-core-data-types)
7. [API Specification](#7-api-specification)
8. [Priority Queue](#8-priority-queue)
9. [Scheduler](#9-scheduler)
10. [Worker Pool](#10-worker-pool)
11. [Prefix Cache](#11-prefix-cache)
12. [Metrics](#12-metrics)
13. [llama.cpp Integration](#13-llamacpp-integration)
14. [Implementation Phases](#14-implementation-phases)
15. [Load Testing Plan](#15-load-testing-plan)
16. [Content and Open Source Plan](#16-content-and-open-source-plan)

---

## 1. What This Is

PriorityServe is a Go HTTP server that sits in front of a llama.cpp server and adds **priority-aware request scheduling**. It exposes an OpenAI-compatible API. Clients tag requests with an `X-Priority` header (`high`, `medium`, or `low`). The scheduler always drains high-priority requests before medium, and medium before low.

The project answers one specific question:

> *Can priority-aware request scheduling produce measurable, reliable p95 latency separation between tiers on consumer hardware (Apple Silicon)?*

It is not trying to replace Ollama or vLLM. It is a focused systems experiment with a benchmark suite to prove or disprove the hypothesis.

---

## 2. Goals and Non-Goals

### Goals
- OpenAI-compatible REST API (`/v1/chat/completions`)
- Three-tier priority queue with strict ordering (high > medium > low)
- Configurable worker pool to cap concurrent llama.cpp requests
- LRU prefix cache tracking shared system prompts (cache hit/miss metrics)
- Prometheus metrics: p50/p95 latency per tier, queue depth per tier, tokens/sec
- Load test harness: 50 concurrent clients, mixed priority distribution
- Clean README, reproducible benchmark results

### Non-Goals
- Token-level continuous batching (requires deep inference engine integration — out of scope)
- Multi-model routing
- Authentication / API keys
- Persistent request storage
- Windows support

---

## 3. How It Works — Big Picture

```
┌─────────────────────────────────────────────────────┐
│                    CLIENT                           │
│  POST /v1/chat/completions                          │
│  X-Priority: high | medium | low                    │
└───────────────────┬─────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────┐
│              HTTP SERVER (Go, :8080)                │
│  - Parse X-Priority header                          │
│  - Build InferenceRequest                           │
│  - Push to priority queue                           │
│  - Wait for result on per-request channel           │
└───────────────────┬─────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────┐
│              PRIORITY QUEUE                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐          │
│  │   HIGH   │  │  MEDIUM  │  │   LOW    │          │
│  │ (heap)   │  │  (heap)  │  │  (heap)  │          │
│  └──────────┘  └──────────┘  └──────────┘          │
│   Always drained first                              │
└───────────────────┬─────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────┐
│              SCHEDULER (goroutine)                  │
│  - Poll queues: high → medium → low                 │
│  - Check prefix cache before dispatch               │
│  - Send to worker pool                              │
└───────────────────┬─────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────┐
│              WORKER POOL (N goroutines)             │
│  - N = configurable (default 2 on M1)               │
│  - Each worker forwards to llama.cpp server         │
│  - Streams response back through result channel     │
└───────────────────┬─────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────┐
│         llama.cpp SERVER (localhost:8081)           │
│  - Runs separately, started before PriorityServe    │
│  - OpenAI-compatible API                            │
│  - Metal acceleration on Apple Silicon              │
└─────────────────────────────────────────────────────┘
```

**Request lifecycle:**
1. Client sends `POST /v1/chat/completions` with `X-Priority: high`
2. Handler creates an `InferenceRequest` with a result channel, pushes to high-priority queue
3. Handler blocks, waiting on the result channel (with request context for cancellation)
4. Scheduler loop: pop from highest non-empty queue → dispatch to worker pool
5. Worker checks prefix cache → forwards request to llama.cpp → streams tokens back
6. Worker sends result to the per-request channel
7. Handler streams response to client
8. Metrics recorded: queue wait time, inference time, tokens generated, tier label

---

## 4. Repository Layout

```
priorityserve/
├── cmd/
│   └── priorityserve/
│       └── main.go              # Entry point: wire everything together, start server
├── internal/
│   ├── config/
│   │   └── config.go            # Config struct, load from env + flags
│   ├── scheduler/
│   │   ├── queue.go             # PriorityQueue: heap-backed, thread-safe
│   │   ├── scheduler.go         # Scheduler: goroutine that drains queues into worker pool
│   │   └── scheduler_test.go    # Unit tests: ordering guarantees under concurrent push
│   ├── worker/
│   │   ├── pool.go              # WorkerPool: N goroutines, each calls llama.cpp
│   │   └── pool_test.go         # Unit tests: concurrency limit, cancellation
│   ├── cache/
│   │   ├── prefix.go            # LRU cache keyed on system prompt hash
│   │   └── prefix_test.go       # Unit tests: eviction, hit/miss counting
│   ├── handler/
│   │   ├── chat.go              # POST /v1/chat/completions
│   │   └── health.go            # GET /health
│   └── metrics/
│       └── metrics.go           # Prometheus metric definitions and helpers
├── bench/
│   ├── load_test.go             # Go load test: k concurrent clients, mixed priority
│   └── scenarios.go             # Pre-defined load scenarios (10/25/50 clients)
├── scripts/
│   ├── start_llamacpp.sh        # Helper: download model + start llama.cpp server
│   └── run_bench.sh             # Helper: run full benchmark suite, save results
├── monitoring/
│   ├── docker-compose.yml       # Prometheus + Grafana
│   ├── prometheus.yml           # Scrape config
│   └── grafana/
│       └── dashboard.json       # Pre-built dashboard: latency per tier, queue depth
├── results/                     # Benchmark output (committed to repo)
│   └── .gitkeep
├── go.mod
├── go.sum
├── DESIGN.md                    # This file
└── README.md
```

---

## 5. Configuration

All config via environment variables with sensible defaults. Loaded at startup into a `Config` struct.

```go
// internal/config/config.go

type Config struct {
    // Server
    ListenAddr     string        // default: ":8080"
    ReadTimeout    time.Duration // default: 30s
    WriteTimeout   time.Duration // default: 120s  (long for streaming)

    // llama.cpp backend
    BackendURL     string        // default: "http://localhost:8081"
    BackendTimeout time.Duration // default: 300s

    // Scheduler
    WorkerCount    int           // default: 2  (conservative for M1 RAM)
    QueueDepth     int           // default: 100  (per tier)

    // Prefix cache
    CacheSize      int           // default: 128  (number of system prompts to cache)

    // Metrics
    MetricsAddr    string        // default: ":9090"
}
```

**Environment variables:**

| Variable | Default | Description |
|---|---|---|
| `PS_LISTEN_ADDR` | `:8080` | PriorityServe listen address |
| `PS_BACKEND_URL` | `http://localhost:8081` | llama.cpp server URL |
| `PS_WORKER_COUNT` | `2` | Concurrent llama.cpp requests |
| `PS_QUEUE_DEPTH` | `100` | Max queued requests per tier |
| `PS_CACHE_SIZE` | `128` | LRU cache entries |
| `PS_METRICS_ADDR` | `:9090` | Prometheus metrics address |

---

## 6. Core Data Types

```go
// Priority levels — ordered so high > medium > low numerically
type Priority int

const (
    PriorityHigh   Priority = 3
    PriorityMedium Priority = 2
    PriorityLow    Priority = 1
)

func ParsePriority(s string) (Priority, error) {
    switch strings.ToLower(s) {
    case "high":
        return PriorityHigh, nil
    case "medium", "med":
        return PriorityMedium, nil
    case "low":
        return PriorityLow, nil
    default:
        return PriorityMedium, nil  // default to medium if header absent
    }
}

// InferenceRequest is the unit of work passed through the system
type InferenceRequest struct {
    ID          string          // UUID, for tracing
    Priority    Priority
    Body        []byte          // raw JSON body from client (OpenAI format)
    Stream      bool            // whether client requested streaming
    ResultChan  chan Result      // scheduler writes result here; handler reads
    EnqueuedAt  time.Time       // for queue wait time metric
    Ctx         context.Context // client's request context (cancellation)
}

// Result is what the worker sends back
type Result struct {
    StatusCode int
    Body       []byte          // full response body (non-streaming)
    Stream     <-chan []byte    // token chunks (streaming)
    Err        error
}
```

---

## 7. API Specification

### POST /v1/chat/completions

Accepts standard OpenAI chat completions format. PriorityServe reads the `X-Priority` header and otherwise proxies the request to llama.cpp unchanged.

**Request:**
```
POST /v1/chat/completions
Content-Type: application/json
X-Priority: high           ← new header; optional, defaults to medium

{
  "model": "llama3.2",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "What is 2 + 2?"}
  ],
  "stream": true,
  "max_tokens": 256
}
```

**Response (streaming):**
```
HTTP/1.1 200 OK
Content-Type: text/event-stream

data: {"id":"...","choices":[{"delta":{"content":"4"},...}]}

data: [DONE]
```

**Response (non-streaming):**
```
HTTP/1.1 200 OK
Content-Type: application/json

{
  "id": "...",
  "choices": [{"message": {"role": "assistant", "content": "4"}, ...}],
  "usage": {"prompt_tokens": 23, "completion_tokens": 1, "total_tokens": 24}
}
```

**Error responses:**
```
503 Service Unavailable  — queue is full (X-Priority tier at capacity)
408 Request Timeout      — client context cancelled while waiting in queue
502 Bad Gateway          — llama.cpp returned an error
```

### GET /health

```
HTTP/1.1 200 OK
Content-Type: application/json

{
  "status": "ok",
  "queue_depth": {"high": 0, "medium": 3, "low": 12},
  "workers": {"active": 2, "total": 2}
}
```

### GET /metrics

Standard Prometheus text format on `:9090/metrics`.

---

## 8. Priority Queue

**File:** `internal/scheduler/queue.go`

Three separate heaps, one per tier. The `MultiQueue` exposes a single `Pop()` that returns from the highest-priority non-empty heap.

```go
type MultiQueue struct {
    mu   sync.Mutex
    high heapQueue
    med  heapQueue
    low  heapQueue
    // signal channel: closed/reopened when any item is pushed
    notify chan struct{}
}

// Push adds a request to the appropriate tier queue.
// Returns error if the queue for that tier is at capacity.
func (mq *MultiQueue) Push(req *InferenceRequest) error

// Pop blocks until a request is available, then returns the
// highest-priority pending request. Respects context cancellation.
func (mq *MultiQueue) Pop(ctx context.Context) (*InferenceRequest, error)

// Depths returns current depth of each tier (for metrics + /health).
func (mq *MultiQueue) Depths() (high, med, low int)
```

**Heap ordering:** Within the same tier, requests are ordered by `EnqueuedAt` (FIFO). This prevents starvation within a tier and makes latency measurements predictable.

**Implementation note:** Use `container/heap` from the standard library. `heapQueue` implements `heap.Interface`. The `notify` channel is a standard Go pattern for blocking `Pop` without spinning — push sends to the channel (non-blocking), pop selects on it.

**Starvation note:** Strict priority ordering means low-priority requests can starve indefinitely if high/medium never empties. For the purposes of this project this is acceptable — it is the behavior we want to measure. A production system would add aging, but that is out of scope.

---

## 9. Scheduler

**File:** `internal/scheduler/scheduler.go`

A single goroutine that loops: pop from `MultiQueue` → send to worker pool.

```go
type Scheduler struct {
    queue   *MultiQueue
    pool    *worker.Pool
    metrics *metrics.Metrics
}

func (s *Scheduler) Run(ctx context.Context) {
    for {
        req, err := s.queue.Pop(ctx)
        if err != nil {
            return  // context cancelled, shut down
        }

        // record queue wait time
        s.metrics.RecordQueueWait(req.Priority, time.Since(req.EnqueuedAt))

        // non-blocking send to pool; pool blocks internally until a worker is free
        s.pool.Submit(req)
    }
}
```

The scheduler itself is simple — the complexity lives in the queue (ordering) and the pool (concurrency control). Keep the scheduler loop thin.

**Shutdown:** The scheduler's `ctx` is derived from the server's root context. On SIGINT/SIGTERM, the root context is cancelled, `Pop` unblocks with an error, and the scheduler exits. In-flight workers finish naturally (they hold their own references).

---

## 10. Worker Pool

**File:** `internal/worker/pool.go`

A pool of N goroutines, each waiting on a work channel. Limits concurrent llama.cpp requests to N.

```go
type Pool struct {
    work    chan *scheduler.InferenceRequest
    backend *Backend   // HTTP client pointed at llama.cpp
    metrics *metrics.Metrics
    wg      sync.WaitGroup
}

func NewPool(n int, backend *Backend, m *metrics.Metrics) *Pool

// Submit sends a request to the worker pool.
// Blocks until a worker is available (back-pressure to scheduler).
func (p *Pool) Submit(req *scheduler.InferenceRequest)

// Shutdown waits for all in-flight requests to complete.
func (p *Pool) Shutdown()
```

Each worker goroutine:
```
loop:
  req = <-p.work
  start = now()
  result = p.backend.Do(req)
  p.metrics.RecordInference(req.Priority, time.Since(start), tokensGenerated)
  req.ResultChan <- result
```

**Backend.Do:** Forwards the request body to `llama.cpp` via `http.Client`, handles both streaming and non-streaming responses, respects `req.Ctx` for cancellation.

**Why block on Submit rather than drop:** Back-pressure is the right behavior here. If the pool is saturated, the scheduler pauses, which causes the queue to grow, which lets the handler return 503 when the queue hits its depth limit. This creates a clean feedback loop.

---

## 11. Prefix Cache

**File:** `internal/cache/prefix.go`

Tracks seen system prompts. The purpose is to measure how often requests share a system prompt (a proxy for prefix cache hit rate at the inference level) and to emit that as a metric.

```go
type PrefixCache struct {
    mu      sync.Mutex
    entries map[string]*entry   // key: SHA256 of system prompt content
    order   []string            // LRU order
    maxSize int
    hits    int64
    misses  int64
}

// Check returns true if this system prompt hash was seen recently (cache hit).
// Always records the access (updating LRU order).
func (c *PrefixCache) Check(systemPrompt string) (hit bool)

// Stats returns current hit/miss counts.
func (c *PrefixCache) Stats() (hits, misses int64)
```

**How to extract the system prompt:** Before forwarding to llama.cpp, parse the request body (it's OpenAI JSON). Find the first message with `"role": "system"`. Hash its `"content"` field with SHA256. That's the cache key.

**What "hit" means here:** We are not actually skipping tokenization — llama.cpp handles that internally via its own KV-cache. What we track is whether PriorityServe _saw_ this system prompt before. This lets us report "X% of requests shared a cached system prompt" as a benchmark metric and lets a future version skip redundant processing.

---

## 12. Metrics

**File:** `internal/metrics/metrics.go`

All metrics exposed at `:9090/metrics` in Prometheus format. Use `github.com/prometheus/client_golang`.

```go
// Histograms — per-tier, labeled with priority="high"|"medium"|"low"
QueueWaitSeconds    // time from enqueue to scheduler dispatch
InferenceSeconds    // time from dispatch to first byte back from llama.cpp
TotalLatencySeconds // end-to-end: enqueue to last byte written to client

// Counters
RequestsTotal       // labeled: priority, status (success|error|timeout)
TokensGeneratedTotal // labeled: priority

// Gauges
QueueDepth          // labeled: priority — current depth per tier
ActiveWorkers       // currently executing requests

// Cache
PrefixCacheHitsTotal
PrefixCacheMissesTotal
```

**Histogram buckets for latency:** `[0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60]` seconds. Adjust after first run if your model is faster/slower.

**Grafana dashboard panels to build:**
1. p50 / p95 total latency, one line per priority tier (line chart, last 15m)
2. Queue depth per tier over time (stacked area chart)
3. Active workers (gauge)
4. Request rate per tier (counter rate)
5. Prefix cache hit rate (hits / (hits + misses))
6. Tokens/sec (counter rate on TokensGeneratedTotal)

---

## 13. llama.cpp Integration

PriorityServe does **not** manage llama.cpp as a subprocess. llama.cpp runs as a separate server process, started before PriorityServe. PriorityServe talks to it over HTTP.

**Starting llama.cpp server (script: `scripts/start_llamacpp.sh`):**

```bash
# Download llama.cpp (build from source or use pre-built)
# Download model: Llama 3.2 3B Q4_K_M GGUF from HuggingFace

./llama-server \
  --model ./models/llama-3.2-3b-q4_k_m.gguf \
  --host 127.0.0.1 \
  --port 8081 \
  --n-gpu-layers 999 \    # offload all layers to Metal
  --ctx-size 4096 \
  --parallel 2            # match PS_WORKER_COUNT
```

**Key flag:** `--parallel N` in llama.cpp server controls how many sequences it can handle simultaneously. Set this equal to `PS_WORKER_COUNT`. If PriorityServe sends more concurrent requests than this, llama.cpp will queue them internally — which bypasses our scheduler. Match them.

**Health check:** On startup, PriorityServe should `GET http://localhost:8081/health` and refuse to start if llama.cpp is not ready.

**Timeout:** Set `PS_BACKEND_TIMEOUT=300s`. LLM inference is slow. The Go `http.Client` must not time out on streaming responses mid-stream — use `http.ResponseController` or do not set `ResponseHeaderTimeout` on the transport.

---

## 14. Implementation Phases

Implement strictly in this order. Do not start a phase until the previous one works end-to-end.

---

### Phase 1 — Single Request End-to-End (Week 1-2)

**Goal:** A Go HTTP server that accepts one request and returns an llama.cpp response.

**Tasks:**
1. `go mod init github.com/yourusername/priorityserve`
2. `internal/config/config.go` — load config from env
3. `internal/handler/health.go` — `GET /health` returns 200
4. `internal/worker/pool.go` — single worker, no pool logic yet, just HTTP forward
5. `cmd/priorityserve/main.go` — wire config → handler → worker, start server
6. Manual test: `curl -X POST localhost:8080/v1/chat/completions -d '...'` returns response

**Done when:** You can send a chat request through PriorityServe to llama.cpp and get a response back. Streaming works.

---

### Phase 2 — Priority Queue and Scheduler (Week 3-4)

**Goal:** Requests tagged with `X-Priority` are queued and dispatched in priority order.

**Tasks:**
1. `internal/scheduler/queue.go` — `MultiQueue` with three heaps, `Push`/`Pop`
2. `internal/scheduler/scheduler_test.go` — test that 100 low-priority requests don't block 1 high-priority request from being popped first
3. `internal/scheduler/scheduler.go` — `Scheduler.Run` goroutine
4. `internal/worker/pool.go` — expand to N workers with work channel
5. Update `internal/handler/chat.go` — parse `X-Priority`, push to queue, wait on result channel
6. Wire scheduler into `main.go`

**Done when:** Send 10 low-priority requests then 1 high-priority request; the high-priority request completes first. Verify with logs showing dispatch order.

---

### Phase 3 — Concurrent Dispatch (Week 5-6)

**Goal:** Multiple requests execute concurrently, scheduler enforces tier ordering under contention.

**Tasks:**
1. Expand worker pool to N=2 workers (controlled by `PS_WORKER_COUNT`)
2. Ensure `--parallel` on llama.cpp server matches
3. Test: 5 low-priority requests in flight, then push 1 high-priority — high-priority gets the next available worker slot

**Done when:** Two requests run in parallel (confirm via overlapping log timestamps), and priority order is maintained when a worker frees up.

---

### Phase 4 — Prefix Cache (Week 7)

**Goal:** Track system prompt reuse, emit hit/miss metrics.

**Tasks:**
1. `internal/cache/prefix.go` — LRU cache
2. `internal/cache/prefix_test.go` — eviction at capacity, hit/miss counts
3. Wire into worker: parse system prompt before forwarding, call `cache.Check()`
4. Emit `PrefixCacheHitsTotal` / `PrefixCacheMissesTotal` to Prometheus

**Done when:** Run 20 requests with identical system prompts, 19 show as cache hits in metrics.

---

### Phase 5 — Prometheus + Grafana (Week 7, parallel with Phase 4)

**Goal:** All metrics visible in Grafana.

**Tasks:**
1. `internal/metrics/metrics.go` — define all metrics
2. Wire metric recording into queue, scheduler, worker
3. `monitoring/docker-compose.yml` — Prometheus + Grafana containers
4. `monitoring/prometheus.yml` — scrape `localhost:9090`
5. Import `monitoring/grafana/dashboard.json`

**Done when:** Grafana shows live latency per tier while you send test requests.

---

### Phase 6 — Load Testing (Week 8-9)

**Goal:** Benchmark results that prove or disprove the hypothesis.

**Tasks:**
1. `bench/load_test.go` — parameterized: N clients, priority distribution, duration
2. `bench/scenarios.go` — define scenarios:
   - Baseline: 10 clients, equal priority distribution
   - Skewed: 25 clients, 70% low / 20% medium / 10% high
   - Heavy: 50 clients, 80% low / 15% medium / 5% high
3. `scripts/run_bench.sh` — run all scenarios, write JSON results to `results/`
4. Parse Prometheus for p50/p95 per tier after each scenario

**Metrics to record per scenario:**
- p50 latency per tier
- p95 latency per tier
- Latency separation: `p95(high) / p95(low)` ratio
- Throughput: total tokens/sec
- Queue depth over time (from Prometheus)

**Done when:** You have result files in `results/` for all three scenarios.

---

### Phase 7 — Writeup and Polish (Week 10)

**Tasks:**
1. `README.md` — setup guide, architecture diagram, benchmark results table
2. Results table with actual numbers from Phase 6
3. Fix any bugs found during load testing
4. Tag `v0.1.0`

---

## 15. Load Testing Plan

The load test lives in `bench/load_test.go` and uses standard Go testing + goroutines (no external tool needed, though `hey` or `vegeta` can supplement).

**Client behavior:**
```
for each client goroutine:
  loop for duration:
    priority = sample from distribution
    body = random prompt from prompt_pool (some share system prompts to test cache)
    send POST /v1/chat/completions with X-Priority header
    record: send_time, first_byte_time, last_byte_time, priority, status_code
```

**Prompt pool:** 10 distinct system prompts, 50 user prompts. This gives realistic prefix cache hit rate (~10% unique system prompts means high reuse under load).

**Result collection:** After the run, compute:
- p50/p95 of `last_byte_time - send_time` grouped by priority
- Error rate per tier
- Write to `results/scenario_<name>_<timestamp>.json`

**Expected hypothesis validation:**
- Under heavy load (50 clients, 80% low), `p95(high)` should be significantly lower than `p95(low)`
- If they are similar, the scheduler is not working correctly or the worker pool is too small

---

## 16. Content and Open Source Plan

### GitHub Repository Setup
- Repo name: `priorityserve`
- Description: "Priority-aware request scheduling for local LLM inference on Apple Silicon"
- Topics: `llm`, `inference`, `go`, `llama-cpp`, `scheduling`, `apple-silicon`, `mlops`
- License: MIT
- Include `results/` directory with pre-run benchmark data so visitors see numbers immediately

### README Structure (for visitors)
1. One-sentence pitch + benchmark results table (the hook — show numbers first)
2. "Why this exists" — the vLLM → local gap story (use the explanation from this doc)
3. Architecture diagram
4. Quick start (5 commands to get running)
5. How to run your own benchmarks
6. Results interpretation

### X/Twitter Content Plan

**Video 1 — "The Problem" (post before building)**
- Screen recording: send 10 requests to vanilla Ollama, show they all wait equally
- Narrate: "What if request #1 is a paying user and request #10 is a background job?"
- End with: "I'm building PriorityServe to fix this"

**Video 2 — "The Scheduler" (Week 3-4)**
- Live code walkthrough of `queue.go` — show the heap, explain the Pop logic
- Demo: push 10 low + 1 high, watch logs show high dispatched first
- Keep it under 90 seconds

**Video 3 — "Grafana Dashboard" (Week 7)**
- Screen recording of Grafana under live load
- Two lines: high-priority latency (low, flat) vs low-priority latency (high, variable)
- "This is what latency separation looks like"

**Video 4 — "Benchmark Results" (Week 10)**
- Results table walkthrough
- Under 50 concurrent clients: high p95 = X ms, low p95 = Y ms
- "Priority scheduling works on a MacBook"

**r/LocalLLaMA post (Week 10):**
- Title: "I built a priority scheduler for local LLM inference and measured the latency separation — results inside"
- Body: architecture, methodology, results table, link to repo

### Resume Bullet (final)
> Built PriorityServe, a Go LLM inference server implementing priority-aware request scheduling on llama.cpp; demonstrated [X]ms vs [Y]ms p95 latency separation between SLA tiers under 50 concurrent clients on Apple Silicon M1

Fill in X and Y from actual Phase 6 results.

---

*End of design document. Start with Phase 1. Do not skip phases.*
