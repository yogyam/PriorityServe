# Quickstart

## Prerequisites

- Go 1.22+
- `llama-server` on your PATH
- Model file at `./models/Llama-3.2-3B-Instruct-Q4_K_M.gguf`

### 1. Install Go

Download from [go.dev/dl](https://go.dev/dl) or via Homebrew:

```bash
brew install go
```

### 2. Install llama-server

**macOS (Homebrew) — recommended:**

```bash
brew install llama.cpp
```

**From source (required if you want to customise Metal flags):**

```bash
git clone https://github.com/ggerganov/llama.cpp
cd llama.cpp
cmake -B build -DGGML_METAL=ON
cmake --build build -j --config Release
export PATH="$PATH:$(pwd)/build/bin"
```

### 3. Download the model

```bash
pip install huggingface_hub
hf download bartowski/Llama-3.2-3B-Instruct-GGUF \
  --include '*Q4_K_M*' \
  --local-dir ./models
```

This places `Llama-3.2-3B-Instruct-Q4_K_M.gguf` (~1.9 GB) in `./models/`.

---

## Starting the server

**Terminal 1 — llama.cpp backend**

```bash
./scripts/start_llamacpp.sh
```

Starts llama.cpp on `:8081` with Metal GPU layers and `--parallel` matched to worker count.

**Terminal 2 — PriorityServe**

```bash
go run ./cmd/priorityserve
```

---

## Endpoints

| URL | What it is |
|-----|-----------|
| `http://localhost:8080/v1/chat/completions` | OpenAI-compatible inference API |
| `http://localhost:8080/ui` | Live dashboard (queue depths, workers, latency) |
| `http://localhost:8080/health` | Health check |
| `http://localhost:9090/metrics` | Prometheus metrics |

---

## Sending a request

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Priority: high" \
  -d '{
    "model": "llama3.2",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

`X-Priority` accepts `high`, `medium` (default), or `low`.

---

## Configuration

All settings are environment variables with sensible defaults.

| Variable | Default | Description |
|----------|---------|-------------|
| `PS_LISTEN_ADDR` | `:8080` | API server address |
| `PS_BACKEND_URL` | `http://localhost:8081` | llama.cpp server URL |
| `PS_WORKER_COUNT` | `2` | Concurrent inference workers (match to `--parallel` in llama.cpp) |
| `PS_QUEUE_DEPTH` | `100` | Max queued requests per priority tier |
| `PS_METRICS_ADDR` | `:9090` | Prometheus metrics address |

Example with custom settings:

```bash
PS_WORKER_COUNT=4 PS_QUEUE_DEPTH=50 go run ./cmd/priorityserve
```
