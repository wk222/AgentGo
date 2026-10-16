package bridge

import (
	"context"
	"encoding/json"
	"fmt"

	"agentgo/internal/agent"
)

// ListHarnesses returns the working-mode presets. The frontend, the ACP bridge and the
// gateway all read them from agent.Harnesses(); nothing else defines them.
func (s *AppService) ListHarnesses() map[string]any {
	return map[string]any{
		"success":   true,
		"harnesses": agent.Harnesses(),
		"default":   agent.DefaultHarnessID,
	}
}

// SetSessionHarness applies a preset by id. An empty sessionID changes the default mode
// for new sessions, like SetSessionMode.
func (s *AppService) SetSessionHarness(sessionID, harnessID string) map[string]any {
	h, ok := agent.ResolveHarness(harnessID)
	if !ok {
		return map[string]any{"success": false, "error": fmt.Sprintf("unknown harness %q", harnessID)}
	}
	if sessionID == "" {
		return s.SetSessionMode(string(h.Profile), string(h.Canvas))
	}
	return s.SetSessionModeForSession(sessionID, string(h.Profile), string(h.Canvas))
}

// ListHarnesses implements gateway.HarnessLister.
func (g *RuntimeGateway) ListHarnesses(_ context.Context) ([]byte, error) {
	return json.Marshal(map[string]any{
		"harnesses": agent.Harnesses(),
		"default":   agent.DefaultHarnessID,
	})
}
