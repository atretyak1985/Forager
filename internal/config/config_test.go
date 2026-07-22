package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileIsEmptyNotError(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.MCPServers) != 0 {
		t.Fatalf("servers = %v", f.MCPServers)
	}
}

func TestLoadParsesServers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"mcpServers":{
		"git":{"command":"mcp-server-git","args":["--repo","/workspace"]},
		"ha":{"url":"http://ha:8123/mcp"}}}`), 0644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.MCPServers["git"].Command != "mcp-server-git" || f.MCPServers["ha"].URL != "http://ha:8123/mcp" {
		t.Fatalf("parsed = %+v", f.MCPServers)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{oops"), 0644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error")
	}
}
