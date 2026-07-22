package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/tools"
)

type stubTool struct {
	name   string
	result string
}

func (s stubTool) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: s.name, Description: "stub", Parameters: json.RawMessage(`{"type":"object"}`),
	}}
}

func (s stubTool) Call(context.Context, string) (string, error) { return s.result, nil }

// scriptedLM returns a fake LM Studio serving canned responses in order,
// and a pointer to the captured request bodies.
func scriptedLM(t *testing.T, responses []string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if i >= len(responses) {
			t.Errorf("unexpected request #%d to fake LM", i+1)
			http.Error(w, "no more responses", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, responses[i])
		i++
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

const (
	respToolCall = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"stub","arguments":"{}"}}]}}]}`
	respEmpty    = `{"choices":[{"message":{"role":"assistant","content":""}}]}`
	respFinal    = `{"choices":[{"message":{"role":"assistant","content":"final answer"}}]}`
)

func newTestAgent(url string, maxIter int) *Agent {
	reg := tools.NewRegistry(stubTool{name: "stub", result: "stub result"})
	return New(llm.New(url), reg, Config{Model: "m", MaxIterations: maxIter})
}

func TestRunDispatchesToolsAndReturnsFinal(t *testing.T) {
	srv, bodies := scriptedLM(t, []string{respToolCall, respFinal})
	got, err := newTestAgent(srv.URL, 12).Ask(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if got != "final answer" {
		t.Fatalf("answer = %q", got)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 LM calls, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], `"role":"tool"`) || !strings.Contains((*bodies)[1], "stub result") {
		t.Fatalf("second request missing tool result: %s", (*bodies)[1])
	}
}

func TestRunNudgesOnEmptyContent(t *testing.T) {
	srv, bodies := scriptedLM(t, []string{respEmpty, respFinal})
	got, err := newTestAgent(srv.URL, 12).Ask(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if got != "final answer" {
		t.Fatalf("answer = %q", got)
	}
	if !strings.Contains((*bodies)[1], "final answer now") {
		t.Fatalf("nudge message missing: %s", (*bodies)[1])
	}
}

func TestRunForcesSummaryWhenBudgetExhausted(t *testing.T) {
	srv, bodies := scriptedLM(t, []string{respToolCall, respFinal})
	got, err := newTestAgent(srv.URL, 1).Ask(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if got != "final answer" {
		t.Fatalf("answer = %q", got)
	}
	if !strings.Contains((*bodies)[1], "Tool budget exhausted") {
		t.Fatalf("forced-summary message missing: %s", (*bodies)[1])
	}
}
