// Package config loads the optional forager config file (structures that
// don't fit flags — currently MCP server definitions).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/swarmery/forager/internal/mcp"
)

type File struct {
	MCPServers map[string]mcp.ServerConfig `json:"mcpServers"`
}

// Load reads path; a missing file yields an empty config and no error.
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}
