package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ServerConfig is one entry under "mcpServers" in the forager config file.
// Either Command (stdio transport) or URL (streamable HTTP) must be set.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
}

// Client talks to one MCP server. Calls are serialized (mutex across
// write+read): the agent loop is sequential, so pipelining buys nothing.
type Client struct {
	Name string

	cfg  ServerConfig
	mu   sync.Mutex
	conn io.ReadWriteCloser // stdio transport; nil in HTTP mode
	rd   *bufio.Reader
	proc *exec.Cmd
	init bool
	next int64

	httpc       *http.Client
	httpSession string
}

func NewClient(name string, cfg ServerConfig) *Client {
	return &Client{Name: name, cfg: cfg, httpc: &http.Client{Timeout: 2 * time.Minute}}
}

// NewClientConn wraps an existing transport (tests).
func NewClientConn(name string, conn io.ReadWriteCloser) *Client {
	return &Client{Name: name, conn: conn, rd: bufio.NewReaderSize(conn, 1<<20)}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	if c.proc != nil {
		c.proc.Process.Kill()
		c.proc.Wait()
		c.proc = nil
	}
	c.init = false
}

// stdioConn glues a child process's stdin/stdout into one ReadWriteCloser.
type stdioConn struct {
	io.Reader
	io.WriteCloser
}

func (c *Client) startLocked(ctx context.Context) error {
	if c.cfg.URL != "" {
		return c.initializeLocked(ctx) // HTTP: no process to spawn
	}
	if c.conn == nil {
		cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
		cmd.Env = os.Environ()
		for k, v := range c.cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = os.Stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start mcp server %s: %w", c.Name, err)
		}
		c.proc = cmd
		c.conn = stdioConn{Reader: stdout, WriteCloser: stdin}
		c.rd = bufio.NewReaderSize(c.conn, 1<<20)
	}
	return c.initializeLocked(ctx)
}

func (c *Client) initializeLocked(ctx context.Context) error {
	if c.init {
		return nil
	}
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "forager", "version": "1.0"},
	}
	if _, err := c.callLocked(ctx, "initialize", params); err != nil {
		return fmt.Errorf("initialize %s: %w", c.Name, err)
	}
	if err := c.notifyLocked(ctx, "notifications/initialized"); err != nil {
		return fmt.Errorf("initialized notification %s: %w", c.Name, err)
	}
	c.init = true
	return nil
}

// callLocked sends one request and reads until its response (skipping
// server-initiated messages). c.mu must be held.
func (c *Client) callLocked(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.cfg.URL != "" {
		return c.callHTTP(ctx, method, params)
	}
	c.next++
	id := c.next
	req := jsonrpcRequest{Jsonrpc: "2.0", ID: &id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("write to %s: %w", c.Name, err)
	}

	type readResult struct {
		resp jsonrpcResponse
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		for {
			raw, err := c.rd.ReadBytes('\n')
			if err != nil {
				ch <- readResult{err: fmt.Errorf("read from %s: %w", c.Name, err)}
				return
			}
			var resp jsonrpcResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				continue // garbage line — skip
			}
			if resp.ID != nil && *resp.ID == id && resp.Method == "" {
				ch <- readResult{resp: resp}
				return
			}
			// Server-initiated request/notification: out of scope, skip.
		}
	}()
	select {
	case <-ctx.Done():
		// The reader goroutine stays blocked; the next transport error
		// triggers a restart, which recreates the pipe.
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if r.resp.Error != nil {
			return nil, fmt.Errorf("%s %s: rpc error %d: %s", c.Name, method, r.resp.Error.Code, r.resp.Error.Message)
		}
		return r.resp.Result, nil
	}
}

func (c *Client) notifyLocked(ctx context.Context, method string) error {
	if c.cfg.URL != "" {
		return c.notifyHTTP(ctx, method)
	}
	line, err := json.Marshal(jsonrpcRequest{Jsonrpc: "2.0", Method: method})
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(line, '\n'))
	return err
}

// ListTools connects (if needed) and returns the first page of tools.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.startLocked(ctx); err != nil {
		return nil, err
	}
	raw, err := c.callLocked(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res listToolsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.NextCursor != "" {
		fmt.Fprintf(os.Stderr, "mcp %s: tools/list has more pages (ignored in v1)\n", c.Name)
	}
	return res.Tools, nil
}

// CallTool invokes a tool. Transport errors trigger one restart+retry;
// a result with isError=true is returned as text for the model.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.callToolLocked(ctx, name, args)
	if err != nil && ctx.Err() == nil && c.cfg.URL == "" {
		c.closeLocked()
		if serr := c.startLocked(ctx); serr != nil {
			return "", fmt.Errorf("%v (restart failed: %v)", err, serr)
		}
		return c.callToolLocked(ctx, name, args)
	}
	return out, err
}

func (c *Client) callToolLocked(ctx context.Context, name string, args map[string]any) (string, error) {
	if err := c.startLocked(ctx); err != nil {
		return "", err
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.callLocked(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var res callToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if res.IsError {
		return "tool error: " + res.text(), nil
	}
	return res.text(), nil
}
