package tools

import (
	"context"
	"os"
	"strings"
)

// Bootstrap registers built-in tools and optionally MCP servers from environment.
func Bootstrap(ctx context.Context, r *Registry, workspaceRoot string) error {
	if err := registerGetTime(r); err != nil {
		return err
	}
	if err := registerEcho(r); err != nil {
		return err
	}
	if err := RegisterWorkspaceBoundTools(r, workspaceRoot); err != nil {
		return err
	}
	if err := registerAskUser(r); err != nil {
		return err
	}

	// Web search: DuckDuckGo (free, no API key needed)
	if err := registerDuckDuckGoSearch(r); err != nil {
		_ = err // non-fatal — network may be unavailable at boot
	}
	// Bing search (optional: set BING_SEARCH_API_KEY env var to activate)
	if err := registerBingSearch(r); err != nil {
		_ = err
	}

	// Optional MCP: AGENTGO_MCP_COMMAND="npx" AGENTGO_MCP_ARGS="-y,@modelcontextprotocol/server-filesystem,/path"
	cmd := strings.TrimSpace(os.Getenv("AGENTGO_MCP_COMMAND"))
	if cmd != "" {
		args := strings.Fields(os.Getenv("AGENTGO_MCP_ARGS"))
		_ = r.LoadMCPServer(ctx, "mcp", cmd, args)
	}
	return nil
}

// RegisterWorkspaceBoundTools rebinds tools whose closures capture the current
// workspace root. It is safe to call again after the desktop workspace changes.
func RegisterWorkspaceBoundTools(r *Registry, workspaceRoot string) error {
	if err := registerListWorkspace(r, workspaceRoot); err != nil {
		return err
	}
	if err := registerExecuteBash(r, workspaceRoot); err != nil {
		return err
	}
	if err := registerParseFile(r, workspaceRoot); err != nil {
		_ = err
	}
	return nil
}
