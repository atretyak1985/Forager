package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/swarmery/forager/internal/sandbox"
)

func TestShellRunsCommand(t *testing.T) {
	s := NewShell(&sandbox.Local{Workdir: t.TempDir()}, 16000)
	out, err := s.Call(context.Background(), `{"command":"echo forager"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "forager") {
		t.Fatalf("out = %q", out)
	}
}

func TestShellReportsExitStatus(t *testing.T) {
	s := NewShell(&sandbox.Local{Workdir: t.TempDir()}, 16000)
	out, err := s.Call(context.Background(), `{"command":"exit 2"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[exit status 2]") {
		t.Fatalf("out = %q", out)
	}
}

func TestShellTruncatesLongOutput(t *testing.T) {
	s := NewShell(&sandbox.Local{Workdir: t.TempDir()}, 200)
	out, err := s.Call(context.Background(), `{"command":"seq 1 5000"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 400 || !strings.Contains(out, "chars truncated") {
		t.Fatalf("len=%d out=%q", len(out), out)
	}
	if !strings.Contains(out, "5000") { // tail preserved
		t.Fatalf("tail lost: %q", out)
	}
}

func TestShellRejectsEmptyCommand(t *testing.T) {
	s := NewShell(&sandbox.Local{Workdir: t.TempDir()}, 16000)
	if _, err := s.Call(context.Background(), `{"command":"  "}`); err == nil {
		t.Fatal("expected error")
	}
}
