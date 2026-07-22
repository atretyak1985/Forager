package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarmery/forager/internal/llm"
)

// Workspace maps model-visible /workspace paths to a host directory and
// guards against path traversal. All file tools go through Resolve.
type Workspace struct {
	Root string // host path mounted at /workspace in the sandbox
}

// Resolve turns a model-supplied path ("notes.txt" or "/workspace/notes.txt")
// into a host path, rejecting anything that escapes the workspace.
func (w *Workspace) Resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/workspace" {
		p = "."
	}
	p = strings.TrimPrefix(p, "/workspace/")
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be inside /workspace")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes /workspace")
	}
	return filepath.Join(w.Root, clean), nil
}

// Display renders a host path back in container form for the model.
func (w *Workspace) Display(hostPath string) string {
	rel, err := filepath.Rel(w.Root, hostPath)
	if err != nil || rel == "." {
		return "/workspace"
	}
	return "/workspace/" + filepath.ToSlash(rel)
}

// ---- read_file ----

type ReadFile struct {
	WS       *Workspace
	MaxChars int
}

func NewReadFile(ws *Workspace, maxChars int) *ReadFile {
	if maxChars <= 0 {
		maxChars = 12000
	}
	return &ReadFile{WS: ws, MaxChars: maxChars}
}

func (f *ReadFile) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "read_file",
		Description: "Read a text file from /workspace. Long files are windowed; pass offset to continue.",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path inside /workspace"},
				"offset": map[string]any{"type": "integer", "description": "Character offset to continue reading (default 0)"},
			},
			"required": []string{"path"},
		}),
	}}
}

func (f *ReadFile) Call(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	host, err := f.WS.Resolve(args.Path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(host)
	if err != nil {
		return "", err
	}
	text := string(raw)
	start := args.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(text) {
		return fmt.Sprintf("Offset %d is beyond end of file (total %d chars).", start, len(text)), nil
	}
	end := start + f.MaxChars
	if end >= len(text) {
		return text[start:], nil
	}
	for end > start && !isRuneStart(text[end]) {
		end--
	}
	return text[start:end] + fmt.Sprintf(
		"\n\n[truncated: chars %d-%d of %d; call read_file again with offset=%d]",
		start, end, len(text), end), nil
}

// ---- write_file ----

type WriteFile struct {
	WS *Workspace
}

func NewWriteFile(ws *Workspace) *WriteFile { return &WriteFile{WS: ws} }

func (f *WriteFile) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "write_file",
		Description: "Write a text file inside /workspace (parent directories are created; existing content is replaced).",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path inside /workspace"},
				"content": map[string]any{"type": "string", "description": "Full file content"},
			},
			"required": []string{"path", "content"},
		}),
	}}
}

func (f *WriteFile) Call(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	host, err := f.WS.Resolve(args.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(host, []byte(args.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), f.WS.Display(host)), nil
}

// ---- list_dir ----

type ListDir struct {
	WS *Workspace
}

func NewListDir(ws *Workspace) *ListDir { return &ListDir{WS: ws} }

func (f *ListDir) Definition() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "list_dir",
		Description: "List a directory inside /workspace. Directories end with '/'.",
		Parameters: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path inside /workspace (default: workspace root)"},
			},
		}),
	}}
}

func (f *ListDir) Call(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	host, err := f.WS.Resolve(args.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(host)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		info, _ := e.Info()
		fmt.Fprintf(&b, "%s  (%d bytes)\n", e.Name(), info.Size())
	}
	return b.String(), nil
}
