package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMCPConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "mcp_servers.json")

	content := `{
		"mcpServers": {
			"mock_server": {
				"command": "echo",
				"args": ["hello"],
				"env": { "TEST_ENV": "123" },
				"description": "Mock MCP server for testing"
			},
			"disabled_server": {
				"command": "bad_cmd",
				"disabled": true
			}
		}
	}`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write mock config: %v", err)
	}

	cfg, err := LoadMCPConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadMCPConfigFile failed: %v", err)
	}

	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.MCPServers))
	}

	mockSrv, ok := cfg.MCPServers["mock_server"]
	if !ok {
		t.Fatalf("mock_server not found in config")
	}
	if mockSrv.Command != "echo" || len(mockSrv.Args) != 1 || mockSrv.Args[0] != "hello" {
		t.Errorf("unexpected command/args: %+v", mockSrv)
	}
	if mockSrv.Env["TEST_ENV"] != "123" {
		t.Errorf("unexpected env: %+v", mockSrv.Env)
	}

	reg := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	AutoLoadMCPServers(ctx, reg, tempDir)
	// Server "echo" is not a real MCP JSON-RPC server so it should fail gracefully without panic
	summaries := reg.ListMCPServers()
	if len(summaries) != 0 {
		t.Errorf("expected 0 successful MCP servers, got %d", len(summaries))
	}
}
