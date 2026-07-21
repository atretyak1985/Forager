// Package tools implements the "hands" of the agent: web search and page fetch.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/swarmery/forager/internal/llm"
)

// Tool is a callable capability exposed to the model.
type Tool interface {
	Definition() llm.Tool
	// Call receives the raw JSON arguments produced by the model
	// and returns a plain-text result to feed back into the conversation.
	Call(ctx context.Context, argsJSON string) (string, error)
}

// Registry holds available tools and dispatches calls by name.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(ts))}
	for _, t := range ts {
		r.tools[t.Definition().Function.Name] = t
	}
	return r
}

func (r *Registry) Definitions() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Definition())
	}
	return out
}

func (r *Registry) Dispatch(ctx context.Context, call llm.ToolCall) string {
	t, ok := r.tools[call.Function.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Function.Name)
	}
	res, err := t.Call(ctx, call.Function.Arguments)
	if err != nil {
		// Errors go back to the model as text so it can retry or adapt.
		return fmt.Sprintf("error: %v", err)
	}
	return res
}

func mustSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
