## Highlights
- First stable thin-llama release.
- Lightweight llama.cpp control plane with Ollama-compatible APIs for chat/embed/pull/tags.
- Docker-first deployment with persistent /models and /state volumes.

## API
- /health
- /api/runtime
- /api/tags
- /api/models
- /api/models/active
- /api/chat
- /api/embed
- /api/pull
- /metrics

## Known limitations
- No built-in auth/TLS.
- One supervised process per role (chat, embedding).
- Synchronous model pull flow.
