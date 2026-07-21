// Package proxy exposes an OpenAI-compatible /v1/chat/completions endpoint.
// Clients (curl, chat UIs) talk to forager as if it were LM Studio,
// and forager runs the web-research agent loop underneath.
package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/swarmery/forager/internal/agent"
	"github.com/swarmery/forager/internal/llm"
)

type Server struct {
	Agent *agent.Agent
	Model string // default model reported/used when request doesn't specify one
}

type incomingRequest struct {
	Model    string        `json:"model"`
	Messages []llm.Message `json:"messages"`
	Stream   bool          `json:"stream"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	return mux
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req incomingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if len(req.Messages) == 0 {
		httpError(w, http.StatusBadRequest, "messages must not be empty")
		return
	}
	if req.Stream {
		httpError(w, http.StatusBadRequest, "streaming is not supported; set stream=false")
		return
	}

	// Model passthrough: "google/gemma-4-12b" or "google/gemma-4-12b-web" both work;
	// empty or matching the default alias -> config default.
	model := strings.TrimSuffix(strings.TrimSpace(req.Model), "-web")
	if model == strings.TrimSuffix(s.Model, "-web") {
		model = ""
	}

	start := time.Now()
	answer, _, err := s.Agent.RunModel(r.Context(), model, req.Messages)
	if err != nil {
		log.Printf("agent run failed: %v", err)
		httpError(w, http.StatusBadGateway, "agent error: %v", err)
		return
	}
	log.Printf("request served in %s (model %q)", time.Since(start).Round(time.Millisecond), req.Model)

	reported := s.Model
	if model != "" {
		reported = model + "-web"
	}
	resp := map[string]any{
		"id":      fmt.Sprintf("forager-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   reported,
		"choices": []map[string]any{{
			"index":         0,
			"message":       llm.Message{Role: "assistant", Content: answer},
			"finish_reason": "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": s.Model, "object": "model", "owned_by": "forager"},
		},
	})
}

func httpError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": fmt.Sprintf(format, args...)},
	})
}
