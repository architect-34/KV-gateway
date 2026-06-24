# KV-Cache-Aware LLM Inference Gateway

Standard load balancers (round-robin, least-connections) are "cache-blind" — they distribute requests without knowing how much GPU memory each worker has committed to its KV cache. In LLM inference, a single long-context request can consume orders of magnitude more KV-cache memory than a short one, causing one worker to thrash while others idle. This gateway routes requests by **predicted KV-cache footprint and live telemetry**, keeping workers evenly utilized and cutting tail latency.

## Architecture

```
                    ┌─────────────────────────────┐
                    │     Client (curl / app)      │
                    │  POST /v1/chat/completions   │
                    └──────────┬──────────────────-┘
                               │ HTTP + SSE
                    ┌──────────▼──────────────────-┐
                    │         Gateway               │
                    │  ┌─────────────────────────┐  │
                    │  │   Router (Policy)        │  │
                    │  │  • RoundRobin            │  │
                    │  │  • LeastLoaded           │  │
                    │  │  • KVAware  ◄── headline │  │
                    │  └──────────┬──────────────-┘  │
                    │  ┌──────────▼──────────────-┐  │
                    │  │   Registry               │  │
                    │  │   worker state + health   │  │
                    │  └─────────────────────────-┘  │
                    └──┬──────────┬──────────┬──────-┘
              gRPC     │          │          │    gRPC
            ┌──────────▼┐  ┌─────▼─────┐  ┌▼──────────┐
            │ Worker 1   │  │ Worker 2   │  │ Worker 3   │
            │ KV: ████░░ │  │ KV: ██░░░░ │  │ KV: █░░░░░ │
            │ sim latency│  │ sim latency│  │ sim latency│
            └────────────┘  └───────────-┘  └────────────┘
```

Workers push telemetry (KV usage, active requests) to the gateway every 100ms via gRPC streaming. The gateway's router uses this state to pick the best worker for each incoming request.

## Benchmark Results

**KV-aware routing cuts P95 TTFT by 2.2× vs. round-robin** on a 300-concurrent-user workload (80% short prompts, 20% long prompts).

![Benchmark Chart](bench/results.png)

| Policy | P50 TTFT | P95 TTFT | P99 TTFT | Success Rate |
|--------|----------|----------|----------|-------------|
| round-robin | 91ms | 1128ms | 8632ms | 44% |
| least-loaded | 93ms | 1266ms | 7745ms | 41% |
| **kv-aware** | **59ms** | **510ms** | **7465ms** | **83%** |

KV-aware also nearly doubles the success rate because it avoids routing requests to workers whose KV cache would overflow.

### Why KV-aware beats least-loaded

Least-loaded counts active requests, but one long-context request (2000 tokens) costs 40× more KV memory than a short one (50 tokens). Least-loaded can't see that difference — KV-aware can.

## Quick Start

```bash
# With Docker
docker compose up

# Without Docker (3 terminals)
go run ./cmd/gateway --policy=kv-aware --http-port=8080 --grpc-port=9000
go run ./cmd/worker --id=w1 --port=9091 --gateway=localhost:9000 --advertise=localhost:9091
go run ./cmd/worker --id=w2 --port=9092 --gateway=localhost:9000 --advertise=localhost:9092
go run ./cmd/worker --id=w3 --port=9093 --gateway=localhost:9000 --advertise=localhost:9093

# Send a request
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello"}],"max_tokens":20,"stream":true}'
```

## Failover Demo

```bash
# Start system with one unstable worker
go run ./cmd/gateway --policy=kv-aware --http-port=8080 --grpc-port=9000 &
go run ./cmd/worker --id=w1 --port=9091 --gateway=localhost:9000 --advertise=localhost:9091 &
go run ./cmd/worker --id=w2 --port=9092 --gateway=localhost:9000 --advertise=localhost:9092 &
go run ./cmd/worker --id=w3 --port=9093 --gateway=localhost:9000 --advertise=localhost:9093 --fail-after=8 &

# Wait for registration, then send a long request
sleep 5
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Long test"}],"max_tokens":300,"stream":true}'
# Worker w3 dies mid-stream → gateway retries on w1 or w2 → tokens resume
```

## Running the Benchmark

```bash
# Full benchmark (all 3 policies, generates chart)
bash bench/run_bench.sh

# Or manually per-policy:
go run ./bench/loadgen.go --concurrency=300 --requests=600 --policy=kv-aware
python bench/plot.py bench/results.csv bench/results.png
```

## Prior Art

This project is a **small-scale educational reimplementation** of ideas from production KV-cache-aware routing systems:

- **[llm-d](https://github.com/llm-d/llm-d)** — CNCF Sandbox project (co-founded by Google Cloud, Red Hat, IBM, NVIDIA) building a Kubernetes-native LLM inference gateway with KV-cache-aware routing.
- **[NVIDIA Dynamo](https://developer.nvidia.com/dynamo)** — Production inference framework whose KV-router uses a cost function (`overlap_weight × prefill_blocks + decode_blocks`) to route by cache footprint. Our `KVAware` policy is a simplified version of this scoring approach.

This project does **not** claim novelty. It demonstrates the core routing concept in a minimal, self-contained codebase suitable for understanding the problem and the approach.

## Tech Stack

- **Go 1.22+** — gateway, workers, load generator
- **gRPC + Protobuf** — worker telemetry and generation streaming
- **net/http** — OpenAI-compatible SSE API
- **Python + matplotlib** — benchmark plotting
- **Docker Compose** — orchestration

## Project Structure

```
cmd/gateway/       — HTTP server + gRPC telemetry receiver + router
cmd/worker/        — Simulated inference worker with KV-cache latency model
internal/router/   — Policy interface: RoundRobin, LeastLoaded, KVAware
internal/registry/ — Live worker state tracking + health janitor
internal/sim/      — KV-cache simulation + latency model
proto/             — gRPC service definitions
bench/             — Load generator (Go) + plotter (Python)
```
