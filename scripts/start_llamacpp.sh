#!/usr/bin/env bash
# Downloads llama.cpp (if needed) and starts the server on :8081.
# Requires: cmake, make, a GGUF model file.
# Usage: ./scripts/start_llamacpp.sh [path/to/model.gguf]

set -euo pipefail

MODEL="${1:-./models/Llama-3.2-3B-Instruct-Q4_K_M.gguf}"
PORT=8081
# Match this to PS_WORKER_COUNT — if they differ, llama.cpp will re-queue
# requests internally and bypass PriorityServe's scheduler.
PARALLEL="${PS_WORKER_COUNT:-2}"

if [ ! -f "$MODEL" ]; then
  echo "Model not found at $MODEL"
  echo "Download a Llama 3.2 3B Q4_K_M GGUF from HuggingFace and place it at $MODEL"
  echo "Example: huggingface-cli download bartowski/Llama-3.2-3B-Instruct-GGUF --include '*Q4_K_M*' --local-dir ./models"
  exit 1
fi

if ! command -v llama-server &>/dev/null; then
  echo "llama-server not found — build llama.cpp first:"
  echo "  git clone https://github.com/ggerganov/llama.cpp"
  echo "  cd llama.cpp && cmake -B build -DGGML_METAL=ON && cmake --build build -j --config Release"
  echo "  Then add llama.cpp/build/bin to your PATH"
  exit 1
fi

echo "Starting llama.cpp server on :${PORT} with model ${MODEL} (parallel=${PARALLEL})"

llama-server \
  --model "$MODEL" \
  --host 127.0.0.1 \
  --port "$PORT" \
  --n-gpu-layers 999 \
  --ctx-size 4096 \
  --parallel "$PARALLEL" \
  --log-disable
