package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLocalExecCapturesOutputAndExitCode(t *testing.T) {
	l := &Local{Workdir: t.TempDir()}
	res, err := l.Exec(context.Background(), "echo hello; echo err >&2; exit 3", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "hello") || !strings.Contains(res.Output, "err") {
		t.Fatalf("output = %q", res.Output)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
}

func TestLocalExecTimesOut(t *testing.T) {
	l := &Local{Workdir: t.TempDir()}
	res, err := l.Exec(context.Background(), "sleep 5", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatal("expected TimedOut")
	}
}

func TestLocalExecRunsInWorkdir(t *testing.T) {
	dir := t.TempDir()
	l := &Local{Workdir: dir}
	res, err := l.Exec(context.Background(), "pwd", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, dir) {
		t.Fatalf("pwd = %q, want %q", res.Output, dir)
	}
}
