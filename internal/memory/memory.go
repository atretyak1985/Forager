// Package memory gives the agent persistent notes across sessions:
// markdown files under <workspace>/memory with a MEMORY.md index that is
// injected into the agent-profile system prompt.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarmery/forager/internal/llm"
)

type Store struct {
	Dir string // host path, e.g. <workspace>/memory
}

// Index returns MEMORY.md content (capped) for prompt injection, "" if absent.
func (s *Store) Index(maxChars int) string {
	raw, err := os.ReadFile(filepath.Join(s.Dir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	idx := strings.TrimSpace(string(raw))
	if len(idx) > maxChars {
		idx = idx[:maxChars] + "\n... (index truncated)"
	}
	return idx
}

func slugify(s string) string {
	var b strings.Builder
	prevDash := true // avoid leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'а' && r <= 'я', r == 'і', r == 'ї', r == 'є', r == 'ґ':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "note"
	}
	return slug
}

// ---- memory_save ----

type Save struct{ Store *Store }

func NewSave(s *Store) *Save { return &Save{Store: s} }

func (t *Save) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "memory_save",
		Description: "Save a fact or note to long-term memory so it survives future sessions. " +
			"Use a short stable topic; saving the same topic again replaces the note.",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic":   map[string]any{"type": "string", "description": "Short topic, e.g. 'Server IPs'"},
				"content": map[string]any{"type": "string", "description": "The fact/note to remember"},
			},
			"required": []string{"topic", "content"},
		}),
	}}
}

func (t *Save) Call(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Topic   string `json:"topic"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(args.Topic) == "" || strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("topic and content must not be empty")
	}
	if err := os.MkdirAll(t.Store.Dir, 0o755); err != nil {
		return "", err
	}

	file := slugify(args.Topic) + ".md"
	body := fmt.Sprintf("# %s\n\n%s\n", strings.TrimSpace(args.Topic), strings.TrimSpace(args.Content))
	if err := os.WriteFile(filepath.Join(t.Store.Dir, file), []byte(body), 0o644); err != nil {
		return "", err
	}

	// Append to MEMORY.md unless the file is already indexed.
	idxPath := filepath.Join(t.Store.Dir, "MEMORY.md")
	idx, _ := os.ReadFile(idxPath)
	if !strings.Contains(string(idx), "("+file+")") {
		firstLine := strings.SplitN(strings.TrimSpace(args.Content), "\n", 2)[0]
		if len(firstLine) > 80 {
			firstLine = firstLine[:80]
		}
		entry := fmt.Sprintf("- [%s](%s) — %s\n", strings.TrimSpace(args.Topic), file, firstLine)
		if err := os.WriteFile(idxPath, append(idx, []byte(entry)...), 0o644); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("saved to memory/%s", file), nil
}

// ---- memory_search ----

type Search struct{ Store *Store }

func NewSearch(s *Store) *Search { return &Search{Store: s} }

func (t *Search) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "memory_search",
		Description: "Search saved long-term memory notes. Returns matching lines with their files. Use read_file on memory/<file> for full notes.",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Substring to look for (case-insensitive)"},
			},
			"required": []string{"query"},
		}),
	}}
}

func (t *Search) Call(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	q := strings.ToLower(strings.TrimSpace(args.Query))
	if q == "" {
		return "", fmt.Errorf("query is empty")
	}

	entries, err := os.ReadDir(t.Store.Dir)
	if err != nil {
		return "No memory entries yet.", nil
	}
	var b strings.Builder
	matches := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(t.Store.Dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				fmt.Fprintf(&b, "memory/%s: %s\n", e.Name(), strings.TrimSpace(line))
				if matches++; matches >= 50 {
					b.WriteString("... (more matches truncated)\n")
					return b.String(), nil
				}
			}
		}
	}
	if matches == 0 {
		return "No memory entries match that query.", nil
	}
	return b.String(), nil
}

func mustSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
