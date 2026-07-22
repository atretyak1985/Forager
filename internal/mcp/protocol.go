// Package mcp is a minimal Model Context Protocol client: JSON-RPC 2.0 over
// stdio or streamable HTTP, supporting initialize / tools/list / tools/call.
package mcp

import "encoding/json"

const protocolVersion = "2025-06-18"

type jsonrpcRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"` // nil => notification
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method"` // set on server-initiated messages; we skip those
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolInfo describes one tool advertised by an MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor"`
}

type callToolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (r callToolResult) text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return "(tool returned no text content)"
	}
	return joinLines(parts)
}

func joinLines(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += "\n" + p
	}
	return out
}
