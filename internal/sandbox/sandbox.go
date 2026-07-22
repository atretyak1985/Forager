// Package sandbox executes model-requested commands in an isolated environment.
// Production uses Docker (docker exec into a long-lived container); tests use Local.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Result is the outcome of a sandboxed command.
type Result struct {
	Output   string // combined stdout+stderr
	ExitCode int
	TimedOut bool
}

// Runner executes a shell command with a hard timeout.
// err is reserved for infrastructure failures (docker missing, etc.);
// command failure is expressed via ExitCode/TimedOut.
type Runner interface {
	Exec(ctx context.Context, command string, timeout time.Duration) (Result, error)
}

// Docker runs commands inside a long-lived container via `docker exec`.
type Docker struct {
	Container string // e.g. "forager-sandbox"
	Workdir   string // working directory inside the container, e.g. "/workspace"
}

func (d *Docker) Exec(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "exec", "-w", d.Workdir, d.Container, "bash", "-lc", command)
	return run(cctx, cmd)
}

// Local runs commands directly on the host. FOR TESTS ONLY — never wire into serve.
type Local struct {
	Workdir string
}

func (l *Local) Exec(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-lc", command)
	cmd.Dir = l.Workdir
	return run(cctx, cmd)
}

func run(cctx context.Context, cmd *exec.Cmd) (Result, error) {
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()

	res := Result{Output: buf.String()}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("exec: %w", err)
	}
	return res, nil
}
