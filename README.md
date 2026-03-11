![thin-llama banner](assets/thin-llama-banner.png)

# thin-llama

`thin-llama` is a lightweight Go wrapper around `llama.cpp` `llama-server` built for small machines that do not need the full Ollama runtime. It runs one active chat model and one active embedding model, manages them as subprocesses, and exposes a minimal Ollama-compatible API surface for existing apps.

The goal is narrow by design:
- single binary
- single Docker image
- JSON config
- local model catalog and pull flow
- Ollama-compatible endpoints first

This project is intended to replace Ollama for constrained self-hosted setups where you want tighter control over model files, process lifecycle, and runtime overhead.

## Current API surface

- `GET /health`
- `GET /api/tags`
- `POST /api/chat`
- `POST /api/embed`
- `POST /api/pull`
- `GET /metrics`

The wrapper proxies chat and embedding requests to dedicated `llama-server` subprocesses and translates responses into Ollama-like JSON.

## Architecture

```mermaid
flowchart LR
    Client["Client / existing app"] --> API["thin-llama API"]
    API --> Chat["chat llama-server process"]
    API --> Embed["embedding llama-server process"]
    API --> State["JSON state store"]
    API --> Models["configured GGUF catalog"]
```

## Project layout

```text
cmd/thin-llama          CLI entrypoint
internal/cli            subcommands
internal/config         JSON config loading and validation
internal/httpapi        Ollama-compatible HTTP handlers
internal/models         model catalog resolution
internal/pull           local download and checksum verification
internal/runtime        llama-server subprocess supervision
internal/state          persistent JSON state store
internal/metrics        Prometheus metrics
```

## Configuration

Example config: [`config.example.json`](/Users/krzysztofkotlowski/Desktop/thin-llama/config.example.json)

Key fields:
- `listen_addr`
- `state_dir`
- `models_dir`
- `llama_server_bin`
- `active.chat`
- `active.embedding`
- `models[]`

Each configured model declares:
- `name`
- `role`
- `gguf_path`
- optional `source_url`
- optional `sha256`
- `embedding_dims` for embedding models
- runtime settings such as `threads`, `context_size`, `gpu_layers`, `extra_args`, `port`

## Local development

Prerequisites:
- Go `1.26.1`
- Docker if you want the container flow
- a `llama-server` binary available locally or through the container image

Common commands:

```bash
go mod tidy
make fmt
make test
make validate-config
make run
```

List configured models:

```bash
go run ./cmd/thin-llama models --config ./config.example.json
```

Pull a configured model:

```bash
go run ./cmd/thin-llama pull --config ./config.example.json --model all-minilm
```

## Dockerized appliance mode

The Dockerfile builds `thin-llama`, copies in `llama-server`, and starts the wrapper as PID 1 under `tini`.

Volumes:
- `/models`
- `/state`
- `/config`

Run with Docker Compose:

```bash
docker compose up --build
```

The included compose file mounts:
- `./models -> /models`
- `./state -> /state`
- `./config.example.json -> /config/config.json`

## Notes

- The Docker image assumes the official `llama.cpp` server image publishes `/app/llama-server`.
- The current build targets a small self-hosted appliance model, not a multi-user service.
- Only configured catalog models can be pulled in v1. There is no remote model registry lookup.
