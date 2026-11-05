package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MCPServerConfig represents configuration for a single MCP server: a local
// process (command) or a remote one (url, with optional headers).
type MCPServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	// URL and Headers describe a remote server. Type is "http" (Streamable HTTP),
	// "sse" or "stdio"; when a URL is given without a Type, http is tried first and
	// sse second. Header values and the URL may reference ${ENV_VAR}s so that
	// tokens need not be written into the file.
	URL         string            `json:"url,omitempty"`
	Type        string            `json:"type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
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

// How long a server gets to finish its handshake at start-up. A remote one needs
// DNS, TLS and often a cold start on the other side.
const (
	mcpLocalInitTimeout  = 4 * time.Second
	mcpRemoteInitTimeout = 10 * time.Second
)

// AutoLoadMCPServers scans workspace and user data directories for MCP configurations.
// Remote servers connect in parallel, so a slow one costs one timeout in total
// rather than one per server.
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

	claimed := make(map[string]bool)
	var wg sync.WaitGroup
	defer wg.Wait()

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			cfg, err := LoadMCPConfigFile(p)
			if err != nil {
				log.Printf("[agentgo-mcp] warning loading %s: %v", p, err)
				continue
			}

			for name, srv := range cfg.MCPServers {
				if srv.Disabled || claimed[name] {
					continue
				}
				target, err := srv.target()
				if err != nil {
					log.Printf("[agentgo-mcp] skip MCP server %q: %v", name, err)
					continue
				}

				if target.remote() {
					// A name belongs to the first file that defines it.
					claimed[name] = true
					wg.Add(1)
					go func() {
						defer wg.Done()
						loadAutoMCP(ctx, r, name, target, mcpRemoteInitTimeout)
					}()
					continue
				}
				if loadAutoMCP(ctx, r, name, target, mcpLocalInitTimeout) {
					claimed[name] = true
				}
			}
		}
	}
}

func loadAutoMCP(ctx context.Context, r *Registry, name string, target mcpTarget, timeout time.Duration) bool {
	log.Printf("[agentgo-mcp] connecting to MCP server %q...", name)
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := r.loadMCP(initCtx, name, target); err != nil {
		log.Printf("[agentgo-mcp] failed to load MCP server %s: %v", name, err)
		return false
	}
	log.Printf("[agentgo-mcp] successfully registered MCP server %q", name)
	return true
}
