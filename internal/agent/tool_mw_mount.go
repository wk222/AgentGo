package agent

import (
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

// toolMounts declares which tool-call guards an agent already installs as ADK handlers.
//
// Governance and tool-heal each have two possible mount points: an ADK
// ChatModelAgentMiddleware (outer) and a compose ToolMiddleware on the ToolsNode (inner).
// Installing both makes every tool call go through the same guard twice, so every
// agent build path must pick exactly one. Paths that run through BuildADKHandlers
// install both guards as handlers; paths without handlers fall back to the compose mount.
type toolMounts struct {
	GovernanceInHandlers bool
	HealInHandlers       bool
}

// composeToolMiddlewares returns the compose-level tool middlewares that are NOT
// already covered by ADK handlers (see toolMounts).
func (r *Runner) composeToolMiddlewares(policy governance.Policy, m toolMounts) []compose.ToolMiddleware {
	var mw []compose.ToolMiddleware
	if r.queue != nil && !m.GovernanceInHandlers {
		mw = append(mw, governance.ComposeToolMiddleware(r.queue, policy))
	}
	if !m.HealInHandlers {
		mw = append(mw, ComposeToolHealMiddleware())
	}
	return mw
}
