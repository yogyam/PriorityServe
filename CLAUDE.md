# PriorityServe — Claude Code Context

## What This Project Is

PriorityServe is a priority-aware request scheduler for local LLM inference. It sits in front of a llama.cpp server and routes incoming requests through a three-tier priority queue (high / medium / low), so high-priority users always get served first regardless of how many low-priority batch jobs are queued behind them.

The core research question: **can request-level priority scheduling produce measurable, reliable latency separation between SLA tiers on consumer hardware?**

The answer from our benchmarks: yes — 9x p95 latency separation (high: 4.7s vs low: 41.8s) under 25 concurrent clients on Apple M1.

---

## Architecture

```
Client (X-Priority: high/medium/low)
        ↓
  HTTP Server :8080          (cmd/priorityserve/main.go)
        ↓
  MultiQueue                 (internal/scheduler/queue.go)
  [high] [medium] [low]
        ↓
  Scheduler goroutine        (internal/scheduler/scheduler.go)
  (strict tier order)
        ↓
  Worker Pool (N goroutines) (internal/worker/pool.go)
        ↓
  llama.cpp backend :8081    (internal/worker/backend.go)
        ↓
  Prometheus metrics :9090   (internal/metrics/metrics.go)
```

---

## Key Files

| File | What it does |
|------|-------------|
| `internal/scheduler/queue.go` | Three-tier FIFO queue, `Push`, `Pop` (blocking), and `Age` (starvation prevention) |
| `internal/scheduler/scheduler.go` | Single goroutine that drains the queue and feeds the worker pool |
| `internal/worker/pool.go` | N worker goroutines pulling from an unbuffered channel (back-pressure) |
| `internal/worker/backend.go` | HTTP client proxying requests to llama.cpp |
| `internal/metrics/metrics.go` | Prometheus histograms and counters — `request_latency_seconds`, `queue_depth`, `promotions_total` |
| `internal/handler/chat.go` | POST `/v1/chat/completions` — reads `X-Priority` header, enqueues request, waits for result |
| `internal/handler/ui.go` | SSE-based live dashboard at `/ui` |
| `cmd/loadtest/main.go` | Load test tool — configurable concurrency, priority mix, outputs p50/p95/p99 per tier |

---

## How to Run

```bash
# Terminal 1 — llama.cpp backend (requires llama-server on PATH)
./scripts/start_llamacpp.sh

# Terminal 2 — PriorityServe
go run ./cmd/priorityserve
```

Endpoints:
- `http://localhost:8080/v1/chat/completions` — OpenAI-compatible API
- `http://localhost:8080/ui` — live dashboard
- `http://localhost:9090/metrics` — Prometheus

## How to Test

```bash
go test ./...
```

Scheduler tests cover: tier ordering, FIFO within tier, queue full, blocking Pop, context cancellation, and aging (starvation prevention).

## How to Load Test

```bash
go run ./cmd/loadtest/ -n 120 -c 50 -high 10 -med 20
```

Results are saved as JSON to `results/`. Generate the benchmark chart:

```bash
python3 scripts/plot_benchmark.py
```

---

## Design Decisions Worth Knowing

**Why an unbuffered worker channel?**
The scheduler sends to the worker pool via an unbuffered channel (`make(chan *InferenceRequest)`). This means the scheduler blocks on send until a worker slot is free. The queue absorbs the back-pressure. If we used a buffered channel, the scheduler could race ahead of actual capacity, and priority ordering would break — requests could be dispatched out of order.

**Why process medium→high aging before low→medium?**
In `queue.go`'s `Age()` method, we promote medium→high first. If we did it the other way, a request promoted from low→medium in the same tick could immediately be eligible for medium→high promotion, effectively jumping two tiers in one second. Processing in reverse order means a just-promoted request waits at least one more tick before the next possible promotion.

**Why separate Prometheus port (:9090)?**
The metrics endpoint is on a separate server from the API (:8080). This lets you lock down the API port to clients while keeping metrics accessible to your internal scraper without mixing concerns.

**Why buffer size 1 on ResultChan?**
Each `InferenceRequest` has a `ResultChan chan Result` with buffer size 1. The worker writes once and moves on — it never blocks waiting for the handler to read. Without the buffer, a slow or disconnected client would stall the worker.

---

## Starvation Prevention

Strict priority scheduling has a known failure mode: sustained high-priority traffic starves low-priority requests indefinitely.

We solve this with wait-time aging. A background goroutine runs every second and promotes requests that have waited past a threshold:

- `low → medium` after `PS_AGE_LOW_TO_MED` (default 30s)
- `medium → high` after `PS_AGE_MED_TO_HIGH` (default 60s)

Under a 100-client burst test, all 84 low-priority requests were promoted and the `promotions_total` Prometheus counter confirmed the transitions. Every request is guaranteed service within `AGE_LOW_TO_MED + AGE_MED_TO_HIGH` seconds regardless of high-priority load.

Notable finding: aging solved the starvation problem but did not reduce tail latency under full saturation. When all requests age up, they compete at higher tiers anyway. The value is the **guarantee**, not the average case.

---

## Benchmark Results

| Concurrency | High p95 | Medium p95 | Low p95   | Separation |
|-------------|----------|------------|-----------|------------|
| 10 clients  | 4,500ms  | 4,713ms    | 16,065ms  | 3.6x       |
| 25 clients  | 4,654ms  | 9,168ms    | 41,813ms  | **9.0x**   |
| 50 clients  | 9,472ms  | 19,396ms   | 59,901ms  | 6.3x       |
| 75 clients  | 9,657ms  | 27,483ms   | 95,452ms  | 9.9x       |
| 100 clients | 13,172ms | 34,685ms   | 115,579ms | 8.8x       |

Separation holds at 6–10x across all concurrency levels. High p95 grows from 4.5s to 13s across a 10x increase in load while low p95 grows from 16s to 116s.

---

## What Not to Change Without Understanding

- **`PS_WORKER_COUNT` must match `--parallel` in `start_llamacpp.sh`**. If they differ, llama.cpp will internally re-queue requests and bypass PriorityServe's scheduler, breaking the latency separation guarantee.
- **The scheduler is a single goroutine by design.** Do not parallelize `scheduler.Run()`. Priority ordering is only guaranteed if one goroutine is making dispatch decisions at a time.
