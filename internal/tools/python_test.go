package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/swarmery/forager/internal/sandbox"
)

func pythonFixture(t *testing.T) *Python {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	ws := &Workspace{Root: t.TempDir()}
	// In tests the "container root" is the host workspace itself.
	return NewPython(&sandbox.Local{Workdir: ws.Root}, ws, ws.Root, 16000)
}

func TestPythonRunsCode(t *testing.T) {
	p := pythonFixture(t)
	out, err := p.Call(context.Background(), `{"code":"print(17*23)"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "391") {
		t.Fatalf("out = %q", out)
	}
}

func TestPythonReportsErrors(t *testing.T) {
	p := pythonFixture(t)
	out, err := p.Call(context.Background(), `{"code":"raise ValueError('boom')"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "boom") || !strings.Contains(out, "[exit status 1]") {
		t.Fatalf("out = %q", out)
	}
}

func TestPythonCleansUpScript(t *testing.T) {
	p := pythonFixture(t)
	if _, err := p.Call(context.Background(), `{"code":"print(1)"}`); err != nil {
		t.Fatal(err)
	}
	out, _ := NewListDir(p.WS).Call(context.Background(), `{"path":".tmp"}`)
	if strings.Contains(out, ".py") {
		t.Fatalf("script not cleaned up: %q", out)
	}
}
