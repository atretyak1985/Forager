#!/bin/bash
# End-to-end smoke test for forager. Usage: smoke.sh [--quick] [base-url]
#   --quick : healthz + models only (used by deploy.sh)
#   full    : agent-profile exercises sandbox, python, memory, and MCP (if configured)
set -euo pipefail

QUICK=0
[ "${1:-}" = "--quick" ] && { QUICK=1; shift; }
BASE="${1:-http://localhost:8090}"
WS="${FORAGER_WORKSPACE:-/srv/forager/workspace}"

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

command -v curl >/dev/null || fail "curl not installed"
command -v jq >/dev/null || fail "jq not installed"

MODEL_AGENT=$(curl -sf "$BASE/v1/models" | jq -r '.data[].id' | grep -- '-agent$' | head -1) \
  || fail "cannot list models"
[ -n "$MODEL_AGENT" ] || fail "no -agent model advertised"
curl -sf "$BASE/healthz" >/dev/null || fail "healthz"
echo "ok: healthz + models ($MODEL_AGENT)"
[ "$QUICK" = 1 ] && exit 0

ask() { # ask "<prompt>" -> assistant content
  curl -sf "$BASE/v1/chat/completions" -H 'Content-Type: application/json' \
    -d "$(jq -n --arg m "$MODEL_AGENT" --arg q "$1" \
      '{model:$m, messages:[{role:"user", content:$q}]}')" \
    | jq -r '.choices[0].message.content'
}

STAMP=$(date +%s)

# 1. sandbox file creation
ask "Create the file /workspace/smoke-$STAMP.txt containing exactly: sandbox-ok" >/dev/null
grep -q "sandbox-ok" "$WS/smoke-$STAMP.txt" || fail "sandbox file not created"
echo "ok: run_command/write_file"

# 2. python computation
ask "Use run_python to compute 17*23 and report only the number." | grep -q 391 \
  || fail "python computation"
echo "ok: run_python"

# 3. memory across two independent requests. Local models are non-deterministic
# about calling memory_search, so retry the recall a few times before failing.
ask "Запам'ятай назавжди: кодове слово смок-тесту — barvinok-$STAMP" >/dev/null
recalled=0
for _ in 1 2 3; do
  if ask "Яке кодове слово смок-тесту? Скористайся пам'яттю (memory_search)." | grep -q "barvinok-$STAMP"; then
    recalled=1
    break
  fi
done
[ "$recalled" = 1 ] || fail "memory recall"
echo "ok: memory"

# 4. MCP (only if any mcp_ tool is configured — detected via config file)
if [ -f /etc/forager/config.json ] && jq -e '.mcpServers | length > 0' /etc/forager/config.json >/dev/null 2>&1; then
  ask "List the files in /workspace using an mcp_ tool (not run_command) and say MCP-OK if it worked." \
    | grep -qi "mcp-ok" || fail "mcp tool call"
  echo "ok: mcp"
fi

rm -f "$WS/smoke-$STAMP.txt"
echo "SMOKE PASS"
