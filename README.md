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
  tool-enabled model. The request `model` selects a profile by suffix:
  `<model>-web` (research only) or `<model>-agent` (full toolset). The proxy
  strips the profile suffix and forwards the base model to LM Studio; leave the
  field empty or use the default alias to get the configured default model. See
  [Profiles](#profiles) below.

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
| `FORAGER_WORKSPACE` / `-workspace` | `/srv/forager/workspace` | host path of the shared `/workspace` volume |
| `FORAGER_SANDBOX` / `-sandbox` | `forager-sandbox` | sandbox container name |
| `-profile` | `web` | ask-mode tool profile: `web` or `agent` |
| `-max-iter` | `12` | agent round-trip cap |
| `-fetch-chars` | `12000` | max chars per page read |

Everything binds to localhost by default. Do not expose Forager or SearXNG
externally without auth.

## Tools exposed to the model

**Web profile (`<model>-web`):**
- **`web_search(query, max_results)`** — SearXNG metasearch, numbered results with snippets.
- **`fetch_page(url, offset)`** — readable text of a page, truncated with pagination
  (`offset`) so long articles don't blow the context window of a 14B model.

**Agent profile (`<model>-agent`):**
- All web tools above, plus:
- **`run_command(command, timeout_seconds)`** — execute bash in the isolated sandbox.
- **`run_python(code, timeout_seconds)`** — execute Python 3 in the sandbox.
- **`read_file(path, offset)`** — read workspace files under `/workspace`; offset for pagination.
- **`write_file(path, content)`** — write workspace files under `/workspace`.
- **`list_dir(path)`** — list directory contents under `/workspace`.

## Profiles

- **`<model>-web`** — research mode with web search and page fetch only. Both `/v1/models` lists this, and ask mode defaults to this.
- **`<model>-agent`** — full toolset: research (web search/fetch) + sandbox execution (bash/Python) + file operations (read/write/list under `/workspace`). Request model as `<model>-agent` or use `-profile agent` in ask mode.

The sandbox container (built from `deploy/sandbox`) must be running for the agent profile to work:
```bash
docker compose -f deploy/docker-compose.yml up -d sandbox
```

The workspace directory must exist and be owned by uid 1000 (the sandbox's
`agent` user) so commands run inside the container can write to it — a
root-owned `755` directory would let the container only read, and sandbox-side
writes (`echo x > /workspace/out.txt`) would fail with "permission denied":
```bash
sudo mkdir -p /srv/forager/workspace && sudo chown 1000:1000 /srv/forager/workspace
```

The sandbox container has outbound network access on purpose (so the model can
`git clone`, `pip install`, etc.). It is CPU/memory/pids-limited, runs as a
non-root user with `no-new-privileges` and all Linux capabilities dropped, and
mounts only `/workspace` from the host — but do not treat it as a hostile-code
jail. Keep it, like the rest of forager, on a trusted local host.

## Notes

- Streaming is not supported in serve mode (the agent loop completes before responding).
- Tool errors are fed back to the model as text so it can retry with a different query/URL.
- After `-max-iter` round-trips the model is forced to summarize what it found.
