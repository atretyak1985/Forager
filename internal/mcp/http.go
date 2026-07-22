package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) postHTTP(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	if c.httpSession != "" {
		req.Header.Set("Mcp-Session-Id", c.httpSession)
	}
	return c.httpc.Do(req)
}

func (c *Client) callHTTP(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	body, err := json.Marshal(jsonrpcRequest{Jsonrpc: "2.0", ID: &id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	resp, err := c.postHTTP(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", c.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
		return nil, fmt.Errorf("mcp %s returned HTTP %d", c.Name, resp.StatusCode)
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.httpSession = s
	}

	var jr jsonrpcResponse
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "text/event-stream"):
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var candidate jsonrpcResponse
			if json.Unmarshal([]byte(data), &candidate) == nil &&
				candidate.ID != nil && *candidate.ID == id && candidate.Method == "" {
				jr = candidate
				goto done
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("mcp %s: SSE stream error: %w", c.Name, err)
		}
		return nil, fmt.Errorf("mcp %s: SSE stream ended without a matching response", c.Name)
	default:
		if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
			return nil, fmt.Errorf("mcp %s: decode: %w", c.Name, err)
		}
	}
done:
	if jr.Error != nil {
		return nil, fmt.Errorf("%s %s: rpc error %d: %s", c.Name, method, jr.Error.Code, jr.Error.Message)
	}
	return jr.Result, nil
}

func (c *Client) notifyHTTP(ctx context.Context, method string) error {
	body, err := json.Marshal(jsonrpcRequest{Jsonrpc: "2.0", Method: method})
	if err != nil {
		return err
	}
	resp, err := c.postHTTP(ctx, body)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("mcp %s notification %s returned HTTP %d", c.Name, method, resp.StatusCode)
	}
	return nil
}
