# Forager

**Turn any local LM Studio model into a tool-using agent** — web research, an
isolated Linux sandbox (shell + Python), a persistent workspace with long-term
memory, and any MCP server you point it at — all behind a drop-in
OpenAI-compatible API. Single Go binary, **stdlib-only** (zero external
dependencies).

Forager is the *hands and the harness*; your local model is the *brain*. It runs
the full agentic loop — think → call a tool → read the result → repeat — until
the model produces a final answer.

## Architecture

```
  curl · Open WebUI · n8n/Telegram · cron · CLI
                     │
                     │  OpenAI-compatible API  (/v1/chat/completions, /v1/models)
                     ▼
        ┌──────────────────────────────────────────┐
        │           forager serve  :8090           │
        │                                          │
        │   proxy ──▶  agent loop  ◀── tool results│
        │              │      ▲                     │
        └──────────────┼──────┼─────────────────────┘
                       │      │
      ┌────────────────┘      └─────────────────────────────┐
      ▼ prompt + tool defs                    tool calls ▲   │
 LM Studio :1234                                         │   ▼
 (the model / "brain")                        ┌──────────┴───────────┐
                                              │        tools         │
                                              ├──────────────────────┤
                                              │ SearXNG :8888        │ web_search
                                              │ + direct page fetch  │ fetch_page
                                              │                      │
                                              │ sandbox container    │ run_command
                                              │ (docker exec)        │ run_python
                                              │                      │
                                              │ /workspace volume    │ read/write_file, list_dir
                                              │ workspace/memory/    │ memory_save/search
                                              │                      │
                                              │ MCP servers          │ mcp_<server>_<tool>
                                              │ (stdio / HTTP)       │
                                              └──────────────────────┘
```

The agent loop (one `/v1/chat/completions` request):

```
   user message
        │
        ▼
   ask the model  ──────────────────────────▶  no tool calls?  ──▶  final answer
        ▲   │                                                        + source URLs
        │   │ tool_calls
   results │ ▼
        │  execute tool(s):  search · fetch · shell · python ·
        │  files · memory · MCP        (errors go back as text)
        └──────────────────────────────  repeat, up to  -max-iter
```

Two ways to run it:

- **`forager ask "question"`** — one-shot from the CLI (`-profile web|agent`).
- **`forager serve`** — OpenAI-compatible proxy. Point any existing client at
  `http://localhost:8090/v1` instead of LM Studio and it transparently gets a
  tool-enabled model. The request `model` selects a profile by suffix:
  `<model>-web` (research only) or `<model>-agent` (full toolset). The proxy
  strips the suffix and forwards the base model to LM Studio; leave the field
  empty or use the default alias for the configured default model. See
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

### 4. Smoke test

```bash
# Full run — exercises sandbox, Python, memory, and MCP (if configured).
# Optional base URL (defaults to http://localhost:8090); needs curl + jq.
./deploy/smoke.sh [base-url]

# Quick mode (healthz + models only) — also used as the deploy gate in deploy.sh:
./deploy/smoke.sh --quick [base-url]
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
| `FORAGER_CONFIG` / `-config` | `/etc/forager/config.json` | config file for [MCP servers](#mcp-servers) |
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
- **`memory_save(topic, content)`** — save a fact to long-term memory (survives sessions).
- **`memory_search(query)`** — search saved memory notes.
- **`mcp_<server>_<tool>(...)`** — any tool exposed by a configured [MCP server](#mcp-servers).

The saved-memory index (`workspace/memory/MEMORY.md`) is injected into the agent
profile's system prompt on every request, so the model always knows what it has
remembered.

## Profiles

The model-name suffix picks the tool set. Both are advertised by `GET /v1/models`,
so in a chat UI they appear as two selectable "models".

| Capability | `<model>-web` | `<model>-agent` |
|---|:---:|:---:|
| `web_search`, `fetch_page` | ✅ | ✅ |
| `run_command`, `run_python` (sandbox) | — | ✅ |
| `read_file`, `write_file`, `list_dir` | — | ✅ |
| `memory_save`, `memory_search` | — | ✅ |
| `mcp_<server>_<tool>` | — | ✅ |
| needs the sandbox container running | no | **yes** |
| ask-mode flag | *default* | `-profile agent` |

- **`<model>-web`** — research only; the safe default (no code execution, no
  filesystem access). This is what `forager ask` uses unless told otherwise.
- **`<model>-agent`** — the full harness. Requires the sandbox container (below).

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

## MCP servers

The agent profile can call tools from any number of [Model Context
Protocol](https://modelcontextprotocol.io) servers — over **stdio** (a spawned
child process) or **streamable HTTP**. Define them in the config file
(`-config`, default `/etc/forager/config.json`):

```json
{
  "mcpServers": {
    "git":        { "command": "mcp-server-git", "args": ["--repo", "/workspace"] },
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"] },
    "home":       { "url": "http://homeassistant:8123/mcp" }
  }
}
```

Each discovered tool is registered as `mcp_<server>_<tool>` (e.g. `mcp_git_status`).
Servers are connected once at startup:

- A **missing** config file is fine — no MCP tools, no error.
- A server that is **down or misbehaving** is logged as a warning and skipped;
  it never blocks or crashes `serve`.
- Only a **present-but-unparseable** config file is fatal.

## Clients

### Open WebUI

`deploy/docker-compose.yml` includes an `open-webui` service that binds to `127.0.0.1:3000` and points at the Forager proxy on the Docker bridge (`http://172.17.0.1:8090/v1`).

```bash
docker compose -f deploy/docker-compose.yml up -d open-webui
# then open http://localhost:3000
```

To reach it from another machine, tunnel the port:

```bash
ssh -L 3000:localhost:3000 swarmery-server
```

In the Open WebUI model picker, select `<model>-web` for research-only or `<model>-agent` for the full toolset. `WEBUI_AUTH` is disabled because the port binds to localhost only — add a reverse proxy with auth before exposing it externally.

### Telegram via n8n

No custom bot code required. Build a flow with three nodes:

1. **Telegram Trigger** — receives incoming messages.
2. **HTTP Request** — `POST http://172.17.0.1:8090/v1/chat/completions` with body:
   ```json
   {"model":"<model>-agent","messages":[{"role":"user","content":"{{ $json.message.text }}"}]}
   ```
3. **Telegram Send Message** — reply text set to `{{ $json.choices[0].message.content }}`.

### cron / automation

Call the API from any cron job or shell script. Example daily brief:

```bash
0 7 * * * curl -s http://localhost:8090/v1/chat/completions \
  -d @/etc/forager/daily-brief.json \
  | jq -r '.choices[0].message.content' \
  | mail -s "Daily brief" you@example.com
```

## Notes

- Streaming is not supported in serve mode (the agent loop completes before responding).
- Tool errors are fed back to the model as text so it can retry with a different query/URL.
- After `-max-iter` round-trips the model is forced to summarize what it found.
