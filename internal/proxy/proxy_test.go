package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swarmery/forager/internal/agent"
	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/tools"
)

// fakeAgent returns an *agent.Agent whose LM always answers `answer`.
func fakeAgent(t *testing.T, answer string) *agent.Agent {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, answer)
	}))
	t.Cleanup(srv.Close)
	return agent.New(llm.New(srv.URL), tools.NewRegistry(), agent.Config{Model: "m", MaxIterations: 2})
}

func testServer(t *testing.T) *Server {
	return &Server{
		DefaultModel: "qwen3-14b",
		Agents: map[string]*agent.Agent{
			"web":   fakeAgent(t, "from web profile"),
			"agent": fakeAgent(t, "from agent profile"),
		},
	}
}

func postChat(t *testing.T, h http.Handler, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func answerOf(out map[string]any) string {
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	return msg["content"].(string)
}

func TestChatRoutesByProfileSuffix(t *testing.T) {
	h := testServer(t).Handler()
	code, out := postChat(t, h, `{"model":"qwen3-14b-agent","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || answerOf(out) != "from agent profile" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if out["model"] != "qwen3-14b-agent" {
		t.Fatalf("model = %v", out["model"])
	}
}

func TestChatDefaultsToWebProfile(t *testing.T) {
	h := testServer(t).Handler()
	code, out := postChat(t, h, `{"model":"","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || answerOf(out) != "from web profile" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if out["model"] != "qwen3-14b-web" {
		t.Fatalf("model = %v", out["model"])
	}
}

func TestChatRejectsStreaming(t *testing.T) {
	h := testServer(t).Handler()
	code, _ := postChat(t, h, `{"model":"x","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 400 {
		t.Fatalf("code = %d", code)
	}
}

func TestModelsListsBothProfiles(t *testing.T) {
	h := testServer(t).Handler()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "qwen3-14b-web") || !strings.Contains(body, "qwen3-14b-agent") {
		t.Fatalf("body = %s", body)
	}
}
