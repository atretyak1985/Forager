package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/sandbox"
)

// Shell exposes sandboxed command execution as the run_command tool.
type Shell struct {
	Runner   sandbox.Runner
	MaxChars int
}

func NewShell(r sandbox.Runner, maxChars int) *Shell {
	if maxChars <= 0 {
		maxChars = 16000
	}
	return &Shell{Runner: r, MaxChars: maxChars}
}

func (s *Shell) Definition() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "run_command",
			Description: "Run a bash command in an isolated Linux container. Working directory is /workspace " +
				"(persistent between calls). Returns combined stdout+stderr. " +
				"Use for git, package installs, data processing, inspecting files.",
			Parameters: mustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Bash command line to execute",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Max seconds to wait (default 60, max 600)",
					},
				},
				"required": []string{"command"},
			}),
		},
	}
}

type shellArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (s *Shell) Call(ctx context.Context, argsJSON string) (string, error) {
	var args shellArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("command is empty")
	}
	timeout := clampTimeout(args.TimeoutSeconds)
	res, err := s.Runner.Exec(ctx, args.Command, timeout)
	if err != nil {
		return "", err
	}
	return formatResult(res, timeout, s.MaxChars), nil
}

func clampTimeout(seconds int) time.Duration {
	switch {
	case seconds <= 0:
		return 60 * time.Second
	case seconds > 600:
		return 600 * time.Second
	default:
		return time.Duration(seconds) * time.Second
	}
}

func formatResult(res sandbox.Result, timeout time.Duration, maxChars int) string {
	out := truncateMiddle(res.Output, maxChars)
	switch {
	case res.TimedOut:
		out += fmt.Sprintf("\n[command timed out after %s]", timeout)
	case res.ExitCode != 0:
		out += fmt.Sprintf("\n[exit status %d]", res.ExitCode)
	}
	if strings.TrimSpace(out) == "" {
		return "(no output, exit status 0)"
	}
	return out
}

// truncateMiddle keeps the head and tail of s. Unlike fetch_page's offset
// pagination, re-running a command is not idempotent, so we never ask the
// model to "call again with offset" here.
func truncateMiddle(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head, tail := max*3/4, max/4
	for head > 0 && !isRuneStart(s[head]) {
		head--
	}
	t := len(s) - tail
	for t < len(s) && !isRuneStart(s[t]) {
		t++
	}
	return s[:head] + fmt.Sprintf("\n...[%d chars truncated]...\n", t-head) + s[t:]
}
