package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wsFixture(t *testing.T) *Workspace {
	t.Helper()
	return &Workspace{Root: t.TempDir()}
}

func TestWorkspaceResolveRejectsEscapes(t *testing.T) {
	ws := wsFixture(t)
	for _, p := range []string{"../x", "/etc/passwd", "/workspace/../x", "a/../../x"} {
		if _, err := ws.Resolve(p); err == nil {
			t.Errorf("Resolve(%q): expected error", p)
		}
	}
}

func TestWorkspaceResolveAcceptsWorkspacePaths(t *testing.T) {
	ws := wsFixture(t)
	for _, p := range []string{"notes.txt", "/workspace/notes.txt", "a/b/c.md", "."} {
		if _, err := ws.Resolve(p); err != nil {
			t.Errorf("Resolve(%q): %v", p, err)
		}
	}
}

func TestWriteThenReadFile(t *testing.T) {
	ws := wsFixture(t)
	w, r := NewWriteFile(ws), NewReadFile(ws, 12000)
	out, err := w.Call(context.Background(), `{"path":"notes/hello.txt","content":"привіт"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/workspace/notes/hello.txt") {
		t.Fatalf("write out = %q", out)
	}
	got, err := r.Call(context.Background(), `{"path":"notes/hello.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "привіт" {
		t.Fatalf("read = %q", got)
	}
}

func TestReadFileOffsetWindow(t *testing.T) {
	ws := wsFixture(t)
	big := strings.Repeat("a", 300)
	os.WriteFile(filepath.Join(ws.Root, "big.txt"), []byte(big), 0644)
	r := NewReadFile(ws, 100)
	out, err := r.Call(context.Background(), `{"path":"big.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "offset=100") {
		t.Fatalf("no continuation hint: %q", out)
	}
	out2, _ := r.Call(context.Background(), `{"path":"big.txt","offset":200}`)
	if !strings.HasPrefix(out2, "aaa") || strings.Contains(out2, "offset=") {
		t.Fatalf("last window wrong: %q", out2)
	}
}

func TestListDir(t *testing.T) {
	ws := wsFixture(t)
	os.MkdirAll(filepath.Join(ws.Root, "sub"), 0755)
	os.WriteFile(filepath.Join(ws.Root, "f.txt"), []byte("x"), 0644)
	l := NewListDir(ws)
	out, err := l.Call(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sub/") || !strings.Contains(out, "f.txt") {
		t.Fatalf("out = %q", out)
	}
}
