package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MCPServerConfig represents configuration for a single MCP server.
type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Description string            `json:"description,omitempty"`
}

// MCPConfigFile mirrors the standard Cursor / Claude / AgentGo MCP config schema.
type MCPConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// LoadMCPConfigFile parses a JSON file containing mcpServers definitions.
func LoadMCPConfigFile(path string) (*MCPConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse MCP config %s: %w", path, err)
	}
	return &cfg, nil
}

// AutoLoadMCPServers scans workspace and user data directories for MCP configurations.
func AutoLoadMCPServers(ctx context.Context, r *Registry, workspaceRoot string) {
	// Candidate config file locations
	candidates := []string{
		filepath.Join(workspaceRoot, "mcp_servers.json"),
		filepath.Join(workspaceRoot, "agentgo_mcp.json"),
		filepath.Join(workspaceRoot, "agentgo_open_ipa_mcp.json"),
	}

	// Also check home / appdata
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".agentgo", "mcp_servers.json"))
	}

	loadedServers := make(map[string]bool)

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			cfg, err := LoadMCPConfigFile(p)
			if err != nil {
				log.Printf("[agentgo-mcp] warning loading %s: %v", p, err)
				continue
			}

			for name, srv := range cfg.MCPServers {
				if srv.Disabled || srv.Command == "" || loadedServers[name] {
					continue
				}

				// Build env slice
				var envList []string
				if len(srv.Env) > 0 {
					// Inherit current system environment
					envList = append(envList, os.Environ()...)
					for k, v := range srv.Env {
						envList = append(envList, fmt.Sprintf("%s=%s", k, v))
					}
				}

				log.Printf("[agentgo-mcp] connecting to MCP server %q (%s %s)...", name, srv.Command, strings.Join(srv.Args, " "))
				initCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				err := r.LoadMCPServerWithEnv(initCtx, name, srv.Command, srv.Args, envList)
				cancel()

				if err != nil {
					log.Printf("[agentgo-mcp] failed to load MCP server %s: %v", name, err)
				} else {
					loadedServers[name] = true
					log.Printf("[agentgo-mcp] successfully registered MCP server %q", name)
				}
			}
		}
	}
}
