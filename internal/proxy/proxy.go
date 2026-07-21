// Package proxy exposes an OpenAI-compatible /v1/chat/completions endpoint.
// Clients (curl, chat UIs) talk to forager as if it were LM Studio,
// and forager runs the web-research agent loop underneath.
package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/swarmery/forager/internal/agent"
	"github.com/swarmery/forager/internal/llm"
)

type Server struct {
	Agent *agent.Agent
	Model string // reported model name; incoming "model" field is ignored
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

	start := time.Now()
	answer, _, err := s.Agent.Run(r.Context(), req.Messages)
	if err != nil {
		log.Printf("agent run failed: %v", err)
		httpError(w, http.StatusBadGateway, "agent error: %v", err)
		return
	}
	log.Printf("request served in %s", time.Since(start).Round(time.Millisecond))

	resp := map[string]any{
		"id":      fmt.Sprintf("forager-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   s.Model,
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
