package bridge

import (
	"context"
	"sort"
	"strings"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
)

// Guard for the tool catalog (docs/kernel-plugin-consensus.md §9, phase 0).
//
// A tool's visibility, risk and file-changing flag used to live in separate name
// tables (tools.staticToolNames, agent.SessionMode.StaticToolAllowlist,
// governance.defaultRiskTable, the capability bus defaults). They only agree
// because someone remembered to edit all of them. These tests turn each
// agreement into a red light, so adding a tool in one place and forgetting
// another fails here instead of silently hiding the tool from the model.

var (
	guardProfiles = []agent.ModeProfile{agent.ModeAssistant, agent.ModeAppMatrix, agent.ModeAdmin}
	guardCanvases = []agent.ExecutionCanvas{agent.CanvasFocused, agent.CanvasBalanced, agent.CanvasDeep}
)

func registeredToolNames(t *testing.T, rt *Runtime) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, bt := range rt.toolReg.GetAllTools() {
		info, err := bt.Info(context.Background())
		if err == nil && info != nil && info.Name != "" {
			out[info.Name] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no tools registered; the guard would pass vacuously")
	}
	return out
}

// nonAdminAllowlist is the union of the static allowlists of every mode that has
// one. Admin returns nil ("every registered static tool"), so it cannot hide an
// orphan: a tool must be reachable somewhere that is not admin.
func nonAdminAllowlist() map[string]bool {
	union := map[string]bool{}
	for _, p := range guardProfiles {
		for _, c := range guardCanvases {
			for n := range (agent.SessionMode{Profile: p, Canvas: c}).StaticToolAllowlist() {
				union[n] = true
			}
		}
	}
	return union
}

func dynamicallyAllowedSomewhere(name string) bool {
	for _, p := range guardProfiles {
		for _, c := range guardCanvases {
			if (agent.SessionMode{Profile: p, Canvas: c}).AllowsDynamicTool(name) {
				return true
			}
		}
	}
	return false
}

// Every registered tool must be reachable by the model in at least one mode:
// static tools must be on some allowlist, the rest must be dynamically allowed.
func TestEveryRegisteredToolIsReachableInSomeMode(t *testing.T) {
	rt := boot(t)
	allow := nonAdminAllowlist()
	var orphans []string
	for name := range registeredToolNames(t, rt) {
		if tools.IsStaticTool(name) {
			if !allow[name] {
				orphans = append(orphans, name+" (static, on no mode allowlist)")
			}
		} else if !dynamicallyAllowedSomewhere(name) {
			orphans = append(orphans, name+" (dynamic, allowed in no mode)")
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("tools the model can never see:\n  %s\n"+
			"Add the tool to the mode allowlists in internal/agent/mode_policy.go, "+
			"or declare it with tools.Declare{Static: true}.", strings.Join(orphans, "\n  "))
	}
}

// Every name on a mode allowlist must be a real tool. A typo or a tool that was
// renamed or removed otherwise lives on as a dead entry that grants nothing.
func TestModeAllowlistsNameOnlyRegisteredTools(t *testing.T) {
	rt := boot(t)
	reg := registeredToolNames(t, rt)
	var dead []string
	for name := range nonAdminAllowlist() {
		if !reg[name] {
			dead = append(dead, name)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Errorf("mode allowlists name tools that are not registered: %v", dead)
	}
}

// Names the risk table may list although no tool of that name is registered when
// the runtime boots: tools that exist only while a feature or an external tool
// pack is loaded. Anything else in the table must be a registered tool, so the
// table cannot silently accumulate dead entries.
func riskEntryMayBeUnregistered(name string) bool {
	if strings.HasPrefix(name, "open_ipa_") { // OpenIPA RPA tool pack (external)
		return true
	}
	switch name {
	case "execute_dynamic_tool", // created by the dynamic tool runtime
		"mcp_filesystem",                 // MCP filesystem server, when configured
		"send_email", "workflow_trigger": // optional integrations
		return true
	}
	return false
}

func TestRiskTableHasNoDeadEntries(t *testing.T) {
	rt := boot(t)
	reg := registeredToolNames(t, rt)
	pol := governance.BuildPolicy("balanced", t.TempDir())
	var dead []string
	for name := range pol.ToolRiskLevels {
		if !reg[name] && !riskEntryMayBeUnregistered(name) {
			dead = append(dead, name)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Errorf("risk table lists tools that do not exist: %v\n"+
			"Plugin tools declare risk with tools.Declare, not in governance.defaultRiskTable.", dead)
	}
}

// The capability bus must report the same risk as the policy for every tool: one
// answer to "how risky is this tool".
func TestBusRiskMatchesPolicyRisk(t *testing.T) {
	rt := boot(t)
	reg := registeredToolNames(t, rt)
	rt.syncAllCapabilities(context.Background())
	checked := 0
	for _, g := range rt.capBus.List("tool") {
		if !reg[g.Name] {
			continue
		}
		checked++
		if want := toolRiskLabel(g.Name); g.RiskLevel != want {
			t.Errorf("bus grant %s has risk %q, policy says %q", g.ID, g.RiskLevel, want)
		}
	}
	if checked == 0 {
		t.Fatal("no tool grants found on the bus")
	}
}

// Anything that changes files must be at least MEDIUM, whichever table said so.
func TestFileChangingToolsAreAtLeastMedium(t *testing.T) {
	rt := boot(t)
	for name := range registeredToolNames(t, rt) {
		tr, declared := governance.ToolTraitsOf(name)
		if declared && tr.Mutating && governance.StaticRisk(name) == governance.RiskLow {
			t.Errorf("%s changes files but its static risk is LOW", name)
		}
	}
	for _, name := range []string{"replace_workspace_text", "create_workspace_file", "execute_bash", "run_batch_script", "run_uv_skill"} {
		if governance.StaticRisk(name) == governance.RiskLow {
			t.Errorf("built-in %s must not be LOW", name)
		}
	}
}

// tools.Declare publishes visibility, file-changing and risk from one record and
// withdraws all of them together.
func TestDeclareIsOneRecord(t *testing.T) {
	const name = "guard_declared_tool"
	dispose := tools.Declare(tools.Spec{
		Name: name, Source: "test", Static: true, Mutating: true, Risk: governance.RiskHigh,
	})
	if !tools.IsStaticTool(name) {
		t.Error("declared static tool is not static")
	}
	if tr, ok := governance.ToolTraitsOf(name); !ok || !tr.Mutating || tr.Risk != governance.RiskHigh {
		t.Errorf("traits = %+v ok=%v", tr, ok)
	}
	if got := governance.StaticRisk(name); got != governance.RiskHigh {
		t.Errorf("StaticRisk = %s, want HIGH", got)
	}
	dispose()
	if tools.IsStaticTool(name) {
		t.Error("static mark survived dispose")
	}
	if _, ok := governance.ToolTraitsOf(name); ok {
		t.Error("traits survived dispose")
	}
	if got := governance.StaticRisk(name); got != governance.RiskLow {
		t.Errorf("StaticRisk after dispose = %s, want LOW", got)
	}
}
