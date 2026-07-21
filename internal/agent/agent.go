// Package agent runs the tool-use loop:
// model -> tool_calls -> execute -> feed results back -> repeat until final answer.
package agent

import (
	"context"
	"fmt"

	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/tools"
)

const defaultSystemPrompt = `You are a research assistant with web access.

Rules:
- Use web_search to find sources, then fetch_page to read the ones that matter.
- Prefer primary sources. Cross-check important claims across at least two sources.
- Today's information may differ from your training data: trust fetched content over memory for anything recent.
- When done, produce a final answer in the user's language with a short list of source URLs.
- Do not invent URLs or facts. If search fails, say so.`

type Config struct {
	Model         string
	MaxIterations int     // cap on model round-trips (default 12)
	Temperature   float64 // default 0.2 for factual research
	SystemPrompt  string  // override the default research prompt if set
}

type Agent struct {
	llm      *llm.Client
	registry *tools.Registry
	cfg      Config

	// OnEvent, if set, receives progress lines (tool calls, iterations) for logging/UI.
	OnEvent func(format string, args ...any)
}

func New(client *llm.Client, reg *tools.Registry, cfg Config) *Agent {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 12
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	return &Agent{llm: client, registry: reg, cfg: cfg}
}

func (a *Agent) event(format string, args ...any) {
	if a.OnEvent != nil {
		a.OnEvent(format, args...)
	}
}

// Run executes the loop for a full message history (system prompt is prepended
// if the history doesn't start with one) and returns the final assistant message.
func (a *Agent) Run(ctx context.Context, history []llm.Message) (string, []llm.Message, error) {
	msgs := history
	if len(msgs) == 0 || msgs[0].Role != "system" {
		msgs = append([]llm.Message{{Role: "system", Content: a.cfg.SystemPrompt}}, msgs...)
	}

	temp := a.cfg.Temperature

	for i := 0; i < a.cfg.MaxIterations; i++ {
		resp, err := a.llm.Chat(ctx, llm.ChatRequest{
			Model:       a.cfg.Model,
			Messages:    msgs,
			Tools:       a.registry.Definitions(),
			Temperature: &temp,
		})
		if err != nil {
			return "", msgs, err
		}

		choice := resp.Choices[0]
		msgs = append(msgs, choice.Message)

		// No tool calls -> the model produced its final answer.
		if len(choice.Message.ToolCalls) == 0 {
			return choice.Message.Content, msgs, nil
		}

		for _, tc := range choice.Message.ToolCalls {
			a.event("[iter %d] tool %s(%s)", i+1, tc.Function.Name, trim(tc.Function.Arguments, 200))
			result := a.registry.Dispatch(ctx, tc)
			a.event("[iter %d] -> %d chars", i+1, len(result))
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}

	// Budget exhausted: ask the model to wrap up without tools.
	msgs = append(msgs, llm.Message{
		Role:    "user",
		Content: "Tool budget exhausted. Summarize your findings now as a final answer with sources.",
	})
	resp, err := a.llm.Chat(ctx, llm.ChatRequest{
		Model:       a.cfg.Model,
		Messages:    msgs,
		Temperature: &temp,
	})
	if err != nil {
		return "", msgs, fmt.Errorf("final summarization after %d iterations: %w", a.cfg.MaxIterations, err)
	}
	final := resp.Choices[0].Message
	msgs = append(msgs, final)
	return final.Content, msgs, nil
}

// Ask is a convenience wrapper for a single question.
func (a *Agent) Ask(ctx context.Context, question string) (string, error) {
	answer, _, err := a.Run(ctx, []llm.Message{{Role: "user", Content: question}})
	return answer, err
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
