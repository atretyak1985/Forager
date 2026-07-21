# Forager

Web research tools for local LM Studio models. Single Go binary, zero dependencies (stdlib only).

Gives any local model loaded in LM Studio the ability to search the web (via a local SearXNG instance) and read pages, running the full agentic research loop: search → fetch → follow-up → final answer with sources.

## Architecture

```
client (curl / chat UI)
        │  OpenAI-compatible API
        ▼
  forager serve (:8090)           ← agent loop lives here
        │                    ╲
        │ /v1/chat/completions ╲ tool execution
        ▼                       ▼
  LM Studio (:1234)        SearXNG (:8888) + direct page fetch
  (the "brain")            (the "hands")
```

Two modes:

- **`forager ask "question"`** — one-shot CLI research.
- **`forager serve`** — OpenAI-compatible proxy. Point any existing client at
  `http://localhost:8090/v1` instead of LM Studio and it transparently gets a
  web-enabled model (reported as `<model>-web`). The `model` field in the request
  is passed through to LM Studio (with or without the `-web` suffix); leave it
  empty or use the default alias to get the configured default model.

## Setup

### 1. SearXNG (search backend)

```bash
cd deploy
# generate a real secret first:
sed -i "s/CHANGE_ME.*/$(openssl rand -hex 32)\"/" searxng/settings.yml
docker compose up -d
curl "http://localhost:8888/search?q=test&format=json" | head -c 300   # sanity check
```

### 2. LM Studio

Load a tool-calling-capable model (qwen3-14b works well) and start the local
server (default port 1234).

### 3. Build & run Forager

```bash
go build -o forager ./cmd/forager

./forager ask -v "Що нового у світі local LLM за цей тиждень?"

# or as a service:
./forager serve
curl http://localhost:8090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-14b-web","messages":[{"role":"user","content":"Latest ArduPilot release and its changes?"}]}'
```

### Install as systemd service

```bash
go build -o forager ./cmd/forager && sudo cp forager /usr/local/bin/
sudo cp deploy/forager.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now forager
```

## Configuration

| Env / flag | Default | Purpose |
|---|---|---|
| `LMSTUDIO_URL` / `-lm` | `http://localhost:1234/v1` | LM Studio API |
| `SEARXNG_URL` / `-searx` | `http://localhost:8888` | SearXNG instance |
| `FORAGER_MODEL` / `-model` | `qwen3-14b` | model id in LM Studio |
| `FORAGER_LISTEN` / `-listen` | `127.0.0.1:8090` | proxy bind address(es), comma-separated |
| `-max-iter` | `12` | agent round-trip cap |
| `-fetch-chars` | `12000` | max chars per page read |

Everything binds to localhost by default. Do not expose Forager or SearXNG
externally without auth.

## Tools exposed to the model

- **`web_search(query, max_results)`** — SearXNG metasearch, numbered results with snippets.
- **`fetch_page(url, offset)`** — readable text of a page, truncated with pagination
  (`offset`) so long articles don't blow the context window of a 14B model.

## Notes

- Streaming is not supported in serve mode (the agent loop completes before responding).
- Tool errors are fed back to the model as text so it can retry with a different query/URL.
- After `-max-iter` round-trips the model is forced to summarize what it found.
