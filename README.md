![thin-llama banner](assets/thin-llama-banner.png)

# thin-llama

`thin-llama` is a lightweight Go control plane for `llama.cpp` (`llama-server`) on smaller self-hosted machines. It supervises dedicated chat and embedding runtimes, persists local state, and exposes an Ollama-style HTTP API for common app integrations.

This project is aimed at private, resource-constrained deployments where you want:

- a single binary and Docker image
- explicit model lifecycle (`pull` then `use`)
- predictable local GGUF file placement
- a curated built-in catalog with config overrides
- a stable API for chat, embeddings, and runtime/model management

## Quick Start

### Docker (recommended)

Build and run:

```bash
docker build --platform linux/amd64 -t thin-llama:local .

docker run -d \
  --name thin-llama \
  -p 8080:8080 \
  -v thin-llama-models:/models \
  -v thin-llama-state:/state \
  thin-llama:local
```

First checks:

```bash
curl -s http://localhost:8080/health
curl -s http://localhost:8080/api/runtime
curl -s http://localhost:8080/api/models
curl -s http://localhost:8080/api/tags
```

Empty-boot behavior is expected:

- `/health` returns `200` while `runtime_ready` can be `false`
- `/api/models` lists catalog entries, often with `available=false`
- `/api/tags` returns only locally present models

Pull and activate:

```bash
curl -s http://localhost:8080/api/pull \
  -H 'Content-Type: application/json' \
  -d '{"model":"nomic-embed-text"}'

curl -s http://localhost:8080/api/pull \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5:7b"}'

curl -s http://localhost:8080/api/models/active \
  -H 'Content-Type: application/json' \
  -d '{"chat":"qwen2.5:7b","embedding":"nomic-embed-text"}'
```

### Local Binary

Requirements:

- Go `1.26.1`
- reachable `llama-server` binary (or set `llama_server_bin`)

Run:

```bash
go run ./cmd/thin-llama serve --config ./config.local.json
```

## Core Concepts

- **Two managed roles:** one `chat` process and one `embedding` process.
- **Catalog-first model discovery:** built-in catalog from `internal/models/builtin_catalog.json`, then merged with `models[]` overrides by model name.
- **Explicit lifecycle:** models are downloaded via `pull`; activation is done via `use` or `POST /api/models/active`.
- **Boot-empty friendly:** control plane starts even when no models are downloaded.
- **Persistent state:** active selection and process/download state are stored in `/state/state.json` (or `state_dir`).

## CLI Reference

Top-level commands:

- `serve`
- `pull`
- `use`
- `models`
- `validate-config`
- `version`

Key usage:

```bash
thin-llama serve --config ./config.local.json
thin-llama pull --config ./config.local.json --model <name>
thin-llama use --config ./config.local.json --chat <name> --embedding <name>
thin-llama models --config ./config.local.json
thin-llama validate-config --config ./config.local.json
thin-llama version
```

Notes:

- `pull --model` is required.
- `use` requires at least one of `--chat` or `--embedding`.
- `use --api` defaults to `THIN_LLAMA_API` or `http://127.0.0.1:8080`.
- If API is reachable, `use` performs a live switch through `/api/models/active`; otherwise it persists state for next startup.

## API Overview

### Endpoint Matrix

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Runtime/process readiness + active models + build identity |
| `GET` | `/api/runtime` | Runtime metadata and capability list |
| `GET` | `/api/tags` | Locally available models only |
| `GET` | `/api/models` | Full merged catalog + download/runtime diagnostics |
| `POST` | `/api/models/active` | Switch active chat and/or embedding model |
| `POST` | `/api/chat` | Ollama-style chat request, proxied to `/v1/chat/completions` |
| `POST` | `/api/embed` | Ollama-style embeddings request, proxied to `/v1/embeddings` |
| `POST` | `/api/pull` | Download model from configured source URL |
| `GET` | `/metrics` | Prometheus metrics |

### Compatibility Notes

- Chat and embeddings endpoints are Ollama-style, with translation to upstream OpenAI-compatible `llama-server` routes.
- Streaming chat output is returned as `application/x-ndjson` chunks.
- Chat option mapping currently supports `temperature` and `num_predict`/`max_tokens`.
- `/api/tags` intentionally hides models not present on disk.

### Minimal API Examples

```bash
curl -s http://localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5:7b","stream":false,"messages":[{"role":"user","content":"Reply with exactly: thin-llama ok"}]}'
```

```bash
curl -s http://localhost:8080/api/embed \
  -H 'Content-Type: application/json' \
  -d '{"model":"nomic-embed-text","input":["search_query: golang","search_query: vector search"]}'
```

## Configuration

Default config path used by CLI is `config.local.json`.

Tracked files:

- `config.local.json` (runtime default used by this repo and Docker image)
- `config.example.json` (copyable example)

### Config Keys

| Key | Type | Required | Effective default when missing | Purpose |
| --- | --- | --- | --- | --- |
| `listen_addr` | string | yes | `:8080` | API listen address |
| `state_dir` | string | yes | `./state` | Persistent state directory |
| `models_dir` | string | yes | `./models` | Local GGUF storage directory |
| `llama_server_bin` | string | yes | `llama-server` | Path to runtime binary |
| `startup_timeout_seconds` | int | no | `60` | Per-runtime startup timeout |
| `active.chat` | string | no | empty | Initial active chat model |
| `active.embedding` | string | no | empty | Initial active embedding model |
| `models` | array | no | `[]` | Catalog overrides/additions |

Model entry schema (`models[]`):

- required: `name`, `role`, and either `gguf_path` or `source_url`
- optional: `sha256`, `threads`, `context_size`, `gpu_layers`, `extra_args`, `port`
- embedding-only: `embedding_dims` must be positive
- valid roles: `chat`, `embedding`

Environment overrides:

- `THIN_LLAMA_LISTEN_ADDR`
- `THIN_LLAMA_STATE_DIR`
- `THIN_LLAMA_MODELS_DIR`
- `THIN_LLAMA_LLAMA_SERVER_BIN`
- `THIN_LLAMA_API` (CLI `use` target)

## Built-in Catalog

Built-in defaults are curated for small CPU-oriented hosts and currently include:

- chat: `qwen2.5:7b`, `qwen2.5:3b`
- embedding: `nomic-embed-text`, `bge-base-en:v1.5`, `all-minilm`

The embedded catalog can be overridden by name via `models[]` in your config.

## Deployment

### Docker Compose

```bash
docker compose up --build
```

`docker-compose.yml` provisions:

- `8080:8080` API mapping
- named volume `thin-llama-models` mounted at `/models`
- named volume `thin-llama-state` mounted at `/state`
- `restart: unless-stopped`

### Container Behavior

- Entrypoint: `thin-llama serve --config /app/config.local.json`
- Runtime image includes `llama-server` and `thin-llama`
- Exposed API port: `8080`
- Volumes: `/models`, `/state`

## Operations And Troubleshooting

Operational checks:

- `GET /health` for high-level readiness and per-role diagnostics
- `GET /api/models` for model availability, active state, runtime PID, restart/orphan status
- `GET /metrics` for Prometheus counters:
  - `thin_llama_http_requests_total`
  - `thin_llama_model_pulls_total`
  - `thin_llama_proxy_failures_total`

Common issues:

- **`model ... is not downloaded`**: call `POST /api/pull` or `thin-llama pull`.
- **`no active ... model selected`**: set active models with `use` or `POST /api/models/active`.
- **`unmanaged/orphaned process detected`**: conflicting process already owns the runtime port; restart and clear the conflict.
- **upstream `502` from `/api/chat` or `/api/embed`**: target `llama-server` process is not healthy or reachable.
- **restart suppression after rapid failures**: process entered crash-loop protection; inspect model/runtime parameters and recover manually.

Backup/restore:

- persist `/state` and `/models` volumes/directories
- restore both to keep model files and active/process metadata consistent

## Development

Useful targets:

```bash
go mod tidy
make fmt
make test
make validate-config
make models
make pull MODEL=nomic-embed-text
make run
```

## Security And Limitations

Security posture:

- no built-in auth
- no built-in TLS termination
- no multi-tenant isolation

Deploy behind a trusted network boundary (private LAN/VPN and/or reverse proxy with authentication) before broader exposure.

Known functional limits in v1:

- one supervised process per role (`chat`, `embedding`)
- synchronous model pull flow
- partial Ollama compatibility focused on chat/embeddings/pull/tags
- restart suppression after repeated rapid failures
