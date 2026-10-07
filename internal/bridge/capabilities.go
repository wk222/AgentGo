package bridge

import (
	"strings"

	"agentgo/internal/plugin"
)

// Capability is a feature a frontend may offer, and whether it can be used now.
// It is derived from the plugin host's state, never stored, so it cannot drift
// from what is actually running: a screen should show a feature when Available
// and show Reason when not, instead of pretending or guessing.
type Capability struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"` // why it is unavailable
	Plugins   []string `json:"plugins"`          // the plugins it depends on
}

type capabilityDef struct {
	id, title string
	needs     []string
	// hint says how to get the capability when its plugin is not registered at
	// all (optional plugins that are off unless configured).
	hint string
}

var capabilityDefs = []capabilityDef{
	{id: "chat.eino", title: "Eino agent chat", needs: []string{"eino"}},
	{id: "chat.crush", title: "Crush coding engine", needs: []string{"crush"},
		hint: "no Crush executable was found; set AGENTGO_CRUSH_EXE"},
	{id: "protocol.crush", title: "Crush TUI protocol server", needs: []string{"crushproto"},
		hint: "set AGENTGO_CRUSHPROTO_ADDR to serve the Crush protocol"},
	{id: "host.attach", title: "Attach other terminals to this process", needs: []string{"host"},
		hint: "the host endpoint is switched off (AGENTGO_HOSTLINK)"},
	{id: "code.tools", title: "Code-aware tools (LSP, edit, search)", needs: []string{"codetools"}},
	{id: "tools.builtin", title: "Built-in agent tools", needs: []string{"toolset"}},
	{id: "workflow", title: "Workflows", needs: []string{"workflow"}},
	{id: "approvals", title: "Human approval of risky actions", needs: []string{"approvals"}},
	{id: "memory", title: "Long-term memory", needs: []string{"memory"}},
	{id: "memory.distill", title: "Memory distillation", needs: []string{"memory", "distill"}},
	{id: "ide", title: "Workspace files, terminal and git", needs: []string{"ide"}},
	{id: "scheduler", title: "Scheduled tasks", needs: []string{"scheduler"}},
	{id: "gateway", title: "Remote gateway (SSE/HTTP)", needs: []string{"gateway"},
		hint: "set AGENTGO_GATEWAY_ADDR or AGENTGO_GATEWAY_PORT to start the gateway"},
}

// probeCapabilities answers, for each known capability, whether every plugin it
// needs is running, and if not, the first reason found.
func probeCapabilities(statuses []plugin.Status) []Capability {
	by := make(map[string]plugin.Status, len(statuses))
	for _, st := range statuses {
		by[st.Name] = st
	}
	out := make([]Capability, 0, len(capabilityDefs))
	for _, d := range capabilityDefs {
		c := Capability{ID: d.id, Title: d.title, Available: true, Plugins: append([]string{}, d.needs...)}
		for _, need := range d.needs {
			st, ok := by[need]
			if !ok {
				c.Available = false
				c.Reason = "not enabled"
				if d.hint != "" {
					c.Reason += ": " + d.hint
				}
				break
			}
			if st.State == plugin.StateRunning {
				continue
			}
			c.Available = false
			c.Reason = unavailableReason(need, st)
			break
		}
		out = append(out, c)
	}
	return out
}

func unavailableReason(name string, st plugin.Status) string {
	// Disabled is set both by configuration (the plugin never started) and by a
	// user Stop (it started and was stopped); the state tells them apart.
	switch {
	case st.State == plugin.StateFailed:
		return name + " failed to start: " + strings.TrimSpace(st.Error)
	case st.State == plugin.StateSkipped:
		return name + " was skipped, a service it needs is unavailable: " + strings.TrimSpace(st.Error)
	case st.State == plugin.StateStopped:
		return name + " is stopped"
	case st.Disabled:
		return name + " is disabled by configuration"
	default:
		return name + " has not started"
	}
}

// Capabilities lists what the application can do right now and why not where it
// cannot. Frontends use it to show supported features and hide or explain the
// rest.
func (s *AppService) Capabilities() []Capability {
	if s.plugins == nil {
		return probeCapabilities(nil)
	}
	return probeCapabilities(s.plugins.Statuses())
}
