# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Forager gives local LM Studio models web-research capability: an agentic loop of search (via a local SearXNG instance) → fetch pages → follow-up → final answer with sources. It ships as a single Go binary with **zero external dependencies — stdlib only**. Keep it that way: do not add modules to go.mod.

## Commands

```bash
go build -o forager ./cmd/forager     # build
go vet ./... && gofmt -l .            # checks (no linter config beyond stdlib tooling)
go test ./...                         # tests (none exist yet; use standard _test.go files)

./forager ask -v "question"           # one-shot CLI research (-v logs tool calls; optional: -profile agent)
./forager serve                       # OpenAI-compatible proxy on 127.0.0.1:8090

./deploy.sh                           # on the target host: git pull, build, install, restart systemd unit, health-check
```

SearXNG (required search backend) runs locally via `cd deploy && docker compose up -d`. For agent-mode tools, also start the sandbox: `docker compose up -d sandbox`. Sanity-check with `curl "http://localhost:8888/search?q=test&format=json"`. LM Studio must be serving a tool-calling-capable model on :1234.

Configuration is flags-override-env, all defined in `cmd/forager/main.go`: `LMSTUDIO_URL`/`-lm`, `SEARXNG_URL`/`-searx`, `FORAGER_MODEL`/`-model`, `FORAGER_LISTEN`/`-listen` (comma-separated addresses), `FORAGER_WORKSPACE`/`-workspace`, `FORAGER_SANDBOX`/`-sandbox`, `-profile`, `-max-iter`, `-fetch-chars`. Production values live in `deploy/forager.service`.

## Architecture

```
client ──OpenAI API──▶ internal/proxy ──▶ internal/agent (loop) ──▶ internal/llm ──▶ LM Studio :1234
                                              │
                                              └──▶ internal/tools ──▶ SearXNG :8888 / direct HTTP fetch
                                                                  └──▶ internal/sandbox (Docker exec)
```

- **`internal/llm`** — minimal OpenAI-compatible chat-completions client (wire types `Message`, `ToolCall`, `Tool` used across all packages). 10-minute HTTP timeout because local models are slow.
- **`internal/tools`** — the `Tool` interface (`Definition()` + `Call(ctx, argsJSON) (string, error)`) and `Registry` that dispatches by name. Web tools: `web_search` (SearXNG `format=json`) and `fetch_page` (HTML→text via regex, windowed by `offset`; UTF-8-safe slicing). Agent tools: `run_command`, `run_python`, `read_file`, `write_file`, `list_dir` — all isolated in the sandbox container via `docker exec`. Tool errors are deliberately returned to the model as `"error: ..."` text, not propagated — the model retries.
- **`internal/sandbox`** — Docker Runner interface with `Exec()` method; Local implementation for tests. Spawns `docker exec` into the forager-sandbox container at `/workspace` for all code execution and file I/O. Memory-less (state not preserved between calls) — models must read/write files to maintain context across operations.
- **`internal/agent`** — the loop: call model with tool definitions → execute tool calls → append results → repeat. Two escape hatches: an empty-content response with no tool calls gets one "provide your final answer" nudge (thinking models stall this way), and hitting `MaxIterations` forces a tool-less summarization call. `OnEvent` callback is the only logging hook.
- **`internal/proxy`** — OpenAI-compatible server (`POST /v1/chat/completions`, `GET /v1/models`, `GET /healthz`). No streaming (the agent loop must finish before responding; `stream:true` is rejected with 400). Profile selection: request model suffixed with `-web` or `-agent` determines tool set; proxy strips suffix and forwards base model to LM Studio. `splitModelProfile()` handles longest-suffix-wins matching.
- **`cmd/forager`** — flag/env wiring and the `ask`/`serve` subcommands. `serve` can listen on multiple comma-separated addresses (e.g. also on the docker bridge IP so containers can reach it).

Everything binds to localhost by default and there is no auth — never expose Forager or SearXNG externally.
