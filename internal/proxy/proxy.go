// Package proxy exposes an OpenAI-compatible /v1/chat/completions endpoint.
// Clients talk to forager as if it were LM Studio; forager runs the agent
// loop underneath. The model suffix picks the tool profile:
// "<model>-web" (research only) or "<model>-agent" (full toolset).
package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/swarmery/forager/internal/agent"
	"github.com/swarmery/forager/internal/llm"
)

type Server struct {
	Agents       map[string]*agent.Agent // profile name -> agent ("web", "agent")
	DefaultModel string                  // model id in LM Studio, e.g. "qwen3-14b"
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

// splitModelProfile splits "qwen3-14b-agent" into ("qwen3-14b", "agent").
// No known suffix means the default "web" profile with model passthrough,
// so a "web" agent is expected to always be registered. When several profile
// suffixes could match, the longest (most specific) one wins — this keeps the
// result deterministic regardless of map iteration order.
func (s *Server) splitModelProfile(m string) (base, profile string) {
	m = strings.TrimSpace(m)
	best := ""
	for p := range s.Agents {
		suffix := "-" + p
		if strings.HasSuffix(m, suffix) && len(suffix) > len(best) {
			best, profile = suffix, p
		}
	}
	if best != "" {
		return strings.TrimSuffix(m, best), profile
	}
	return m, "web"
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

	base, profile := s.splitModelProfile(req.Model)
	ag, ok := s.Agents[profile]
	if !ok {
		httpError(w, http.StatusBadRequest, "unknown profile %q", profile)
		return
	}
	if base == s.DefaultModel {
		base = "" // config default
	}

	start := time.Now()
	answer, _, err := ag.RunModel(r.Context(), base, req.Messages)
	if err != nil {
		log.Printf("agent run failed: %v", err)
		httpError(w, http.StatusBadGateway, "agent error: %v", err)
		return
	}
	log.Printf("request served in %s (model %q, profile %s)",
		time.Since(start).Round(time.Millisecond), req.Model, profile)

	reported := s.DefaultModel
	if base != "" {
		reported = base
	}
	reported += "-" + profile

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
	profiles := make([]string, 0, len(s.Agents))
	for p := range s.Agents {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)

	data := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		data = append(data, map[string]any{
			"id": s.DefaultModel + "-" + p, "object": "model", "owned_by": "forager",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func httpError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": fmt.Sprintf(format, args...)},
	})
}
