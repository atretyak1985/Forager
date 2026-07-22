package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/tools"
)

// Tools connects to the server and returns its tools wrapped for the
// forager tool Registry.
func (c *Client) Tools(ctx context.Context) ([]tools.Tool, error) {
	infos, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tools.Tool, 0, len(infos))
	for _, info := range infos {
		out = append(out, &registryTool{client: c, info: info})
	}
	return out, nil
}

type registryTool struct {
	client *Client
	info   ToolInfo
}

func (t *registryTool) Definition() llm.Tool {
	name := sanitizeName("mcp_" + t.client.Name + "_" + t.info.Name)
	schema := t.info.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        name,
		Description: t.info.Description,
		Parameters:  schema,
	}}
}

func (t *registryTool) Call(ctx context.Context, argsJSON string) (string, error) {
	args := map[string]any{}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	return t.client.CallTool(ctx, t.info.Name, args)
}

// sanitizeName keeps [A-Za-z0-9_-] (OpenAI function-name charset), max 64.
func sanitizeName(s string) string {
	b := []byte(s)
	for i, ch := range b {
		ok := ch == '_' || ch == '-' ||
			(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		if !ok {
			b[i] = '_'
		}
	}
	if len(b) > 64 {
		b = b[:64]
	}
	return string(b)
}
