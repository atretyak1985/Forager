package tools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/sandbox"
)

// Python writes the script into the shared workspace and executes it in the
// sandbox. Separate from run_command because an explicit tool improves tool
// selection for small models.
type Python struct {
	Runner        sandbox.Runner
	WS            *Workspace
	ContainerRoot string // workspace path as the Runner sees it ("/workspace" in prod)
	MaxChars      int
}

func NewPython(r sandbox.Runner, ws *Workspace, containerRoot string, maxChars int) *Python {
	if maxChars <= 0 {
		maxChars = 16000
	}
	return &Python{Runner: r, WS: ws, ContainerRoot: containerRoot, MaxChars: maxChars}
}

func (p *Python) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_python",
		Description: "Execute a Python 3 script in the sandbox and return its output. Use print() for results. Files in /workspace are accessible.",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":            map[string]any{"type": "string", "description": "Python 3 source code"},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Max seconds (default 60, max 600)"},
			},
			"required": []string{"code"},
		}),
	}}
}

func (p *Python) Call(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Code           string `json:"code"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(args.Code) == "" {
		return "", fmt.Errorf("code is empty")
	}

	var rb [4]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return "", err
	}
	rel := fmt.Sprintf(".tmp/run-%x.py", rb)
	host, err := p.WS.Resolve(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(host, []byte(args.Code), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(host)

	timeout := clampTimeout(args.TimeoutSeconds)
	res, err := p.Runner.Exec(ctx, fmt.Sprintf("python3 %s/%s", p.ContainerRoot, rel), timeout)
	if err != nil {
		return "", err
	}
	return formatResult(res, timeout, p.MaxChars), nil
}
