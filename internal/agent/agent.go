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

// AgentSystemPrompt is the system prompt for the full "agent" tool profile
// (research + sandbox + files). The research-only profile keeps defaultSystemPrompt.
const AgentSystemPrompt = `You are an autonomous assistant with tools: web search (web_search, fetch_page), an isolated Linux sandbox (run_command, run_python), and workspace files (read_file, write_file, list_dir).

Rules:
- Work step by step: one tool call at a time, check its output before deciding the next step.
- All execution happens in an isolated container; persistent files live under /workspace.
- If a tool fails, read the error and change approach instead of repeating the same call.
- Verify your work: list or read files you created, print computed values.
- When done, answer in the user's language and mention any files you created with their /workspace paths.`

type Config struct {
	Model         string
	MaxIterations int     // cap on model round-trips (default 12)
	Temperature   float64 // default 0.2 for factual research
	SystemPrompt  string  // override the default research prompt if set
	// PromptSuffix, if set, is evaluated per run and appended to the system
	// prompt (used to inject the current memory index).
	PromptSuffix func() string
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

// Run executes the loop with the default model from config.
func (a *Agent) Run(ctx context.Context, history []llm.Message) (string, []llm.Message, error) {
	return a.RunModel(ctx, "", history)
}

// RunModel executes the loop with an explicit model; "" means the config default.
func (a *Agent) RunModel(ctx context.Context, model string, history []llm.Message) (string, []llm.Message, error) {
	if model == "" {
		model = a.cfg.Model
	}

	msgs := history
	if len(msgs) == 0 || msgs[0].Role != "system" {
		prompt := a.cfg.SystemPrompt
		if a.cfg.PromptSuffix != nil {
			if extra := a.cfg.PromptSuffix(); extra != "" {
				prompt += "\n\n" + extra
			}
		}
		msgs = append([]llm.Message{{Role: "system", Content: prompt}}, msgs...)
	}

	temp := a.cfg.Temperature

	for i := 0; i < a.cfg.MaxIterations; i++ {
		resp, err := a.llm.Chat(ctx, llm.ChatRequest{
			Model:       model,
			Messages:    msgs,
			Tools:       a.registry.Definitions(),
			Temperature: &temp,
		})
		if err != nil {
			return "", msgs, err
		}

		choice := resp.Choices[0]
		msgs = append(msgs, choice.Message)

		// No tool calls -> the model is done (or stalled with empty content).
		if len(choice.Message.ToolCalls) == 0 {
			if choice.Message.Content != "" {
				return choice.Message.Content, msgs, nil
			}
			// Thinking models sometimes return empty content: nudge once for a final answer.
			a.event("[iter %d] empty content, nudging for final answer", i+1)
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: "Provide your final answer now in plain text, with source URLs.",
			})
			continue
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
		Model:       model,
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
