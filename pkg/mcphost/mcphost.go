// Package mcphost exposes a headless AgentGo backend for external MCP/CLI hosts
// (e.g. agentgo-crush for Charm Crush) without importing agentgo/internal.
package mcphost

import (
	"os"

	"agentgo/internal/bridge"
)

// AppService is the Wails-facing backend; usable headless when no UI is attached.
type AppService = bridge.AppService

// NewHeadless constructs runtime + AppService for stdio MCP servers.
func NewHeadless() (*AppService, error) {
	// Don't run cron in a sidecar process; the desktop app owns scheduling.
	if os.Getenv("AGENTGO_DISABLE_SCHEDULER") == "" {
		_ = os.Setenv("AGENTGO_DISABLE_SCHEDULER", "1")
	}
	rt, err := bridge.NewRuntime()
	if err != nil {
		return nil, err
	}
	return bridge.NewAppService(rt), nil
}
