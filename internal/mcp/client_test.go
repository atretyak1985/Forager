package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type pipeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p pipeConn) Close() error                { p.r.Close(); return p.w.Close() }

// fakeServer speaks newline-delimited JSON-RPC on the returned conn:
// answers initialize, tools/list (one "echo" tool), tools/call (echoes the
// "text" argument; the "fail" tool returns isError:true).
func fakeServer(t *testing.T) io.ReadWriteCloser {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	go func() {
		sc := bufio.NewScanner(serverR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var req jsonrpcRequest
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.ID == nil {
				continue // notification (e.g. notifications/initialized)
			}
			var result string
			switch req.Method {
			case "initialize":
				result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`, protocolVersion)
			case "tools/list":
				result = `{"tools":[{"name":"echo","description":"echoes text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}},{"name":"fail","description":"always errors","inputSchema":{"type":"object"}}]}`
			case "tools/call":
				b, _ := json.Marshal(req.Params)
				var p struct {
					Name string         `json:"name"`
					Args map[string]any `json:"arguments"`
				}
				json.Unmarshal(b, &p)
				if p.Name == "fail" {
					result = `{"isError":true,"content":[{"type":"text","text":"deliberate failure"}]}`
				} else {
					result = fmt.Sprintf(`{"content":[{"type":"text","text":"echo: %v"}]}`, p.Args["text"])
				}
			default:
				continue
			}
			fmt.Fprintf(serverW, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", *req.ID, result)
		}
	}()
	return pipeConn{r: clientR, w: clientW}
}

func newFakeClient(t *testing.T) *Client {
	t.Helper()
	c := NewClientConn("fake", fakeServer(t))
	t.Cleanup(func() { c.Close() })
	return c
}

func TestListTools(t *testing.T) {
	infos, err := newFakeClient(t).ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Name != "echo" {
		t.Fatalf("infos = %+v", infos)
	}
}

func TestCallTool(t *testing.T) {
	out, err := newFakeClient(t).CallTool(context.Background(), "echo", map[string]any{"text": "привіт"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo: привіт" {
		t.Fatalf("out = %q", out)
	}
}

func TestCallToolIsErrorReturnsTextNotFailure(t *testing.T) {
	out, err := newFakeClient(t).CallTool(context.Background(), "fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tool error") || !strings.Contains(out, "deliberate failure") {
		t.Fatalf("out = %q", out)
	}
}

func TestCallContextTimeout(t *testing.T) {
	// A server that reads requests but never answers: the client must honour
	// ctx cancellation. (The write side must be drained — io.Pipe writes
	// block until read, which would deadlock before the ctx check.)
	clientR, _ := io.Pipe() // server never writes
	serverR, clientW := io.Pipe()
	go io.Copy(io.Discard, serverR)
	c := NewClientConn("dead", pipeConn{r: clientR, w: clientW})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallTool(ctx, "echo", nil); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestCancelledCallTearsDownTransport(t *testing.T) {
	// A silent server: the call blocks on read, then ctx is cancelled while
	// waiting. The client must tear the transport down (unblocking and reaping
	// the reader goroutine) rather than leaving it to race a later call. Run
	// under -race to catch a leaked reader sharing the bufio.Reader.
	clientR, _ := io.Pipe()
	serverR, clientW := io.Pipe()
	go io.Copy(io.Discard, serverR)
	c := NewClientConn("silent", pipeConn{r: clientR, w: clientW})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := c.CallTool(ctx, "echo", map[string]any{"text": "x"}); err == nil {
		t.Fatal("expected error on cancelled call")
	}
	// Teardown nils the connection; a NewClientConn client has no Command to
	// respawn, so the next call must fail to reconnect — proving the stale
	// (and now half-closed) pipe was not silently reused.
	if _, err := c.CallTool(context.Background(), "echo", nil); err == nil {
		t.Fatal("expected reconnect failure after teardown")
	}
}

func TestHTTPTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpcRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(202) // notification
			return
		}
		var result string
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{}}`, protocolVersion)
		case "tools/list":
			result = `{"tools":[{"name":"ping","description":"","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") != "sess-1" {
				http.Error(w, "no session", 400)
				return
			}
			result = `{"content":[{"type":"text","text":"pong"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *req.ID, result)
	}))
	defer srv.Close()

	c := NewClient("h", ServerConfig{URL: srv.URL})
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := c.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "pong" {
		t.Fatalf("out = %q", out)
	}
}
