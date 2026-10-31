package bridge

import (
	"os"
	"strings"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// browserTools: what the plugin offers, and whether each acts on the other side.
var browserTools = map[string]bool{
	"browser_open": true, "browser_click": true, "browser_fill": true, "browser_press": true,
	"browser_snapshot": false, "browser_scroll": false, "browser_back": false, "browser_screenshot": false, "browser_close": false,
}

// anyExecutable names a file that exists, so the plugin sees "a browser".
func anyExecutable(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBrowserPluginContributesGovernedTools(t *testing.T) {
	t.Setenv("AGENTGO_BROWSER_PATH", anyExecutable(t))
	t.Setenv("AGENTGO_BROWSER_CONTROL", "")
	s, reg, bus := newCodetoolsService(t, t.TempDir())
	if pluginState(t, s, "browsercontrol") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	for name, acts := range browserTools {
		registryTool(t, reg, name)
		if st, ok := grantStatus(bus, "tool", "browsercontrol", name); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", name, st, ok)
		}
		if !tools.IsStaticTool(name) {
			t.Errorf("%s must be always visible", name)
		}
		tr, ok := governance.ToolTraitsOf(name)
		if !ok || tr.Mutating != acts || tr.Source != "browsercontrol" {
			t.Errorf("%s traits = %+v ok=%v (acts=%v)", name, tr, ok, acts)
		}
		wantRisk := governance.RiskLow
		if acts {
			wantRisk = governance.RiskMedium
		}
		if got := governance.StaticRisk(name); got != wantRisk {
			t.Errorf("%s risk = %s, want %s", name, got, wantRisk)
		}
		for _, g := range bus.List("tool") {
			if want := strings.ToLower(string(wantRisk)); g.Name == name && g.RiskLevel != want {
				t.Errorf("bus risk of %s = %q, want %q", name, g.RiskLevel, want)
			}
		}
		// Offered in the normal modes; in the read-only canvas only the tools
		// that cannot act are.
		open := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()
		if !open[name] {
			t.Errorf("%s not offered in the assistant balanced mode", name)
		}
		focused := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasFocused}.StaticToolAllowlist()
		if focused[name] == acts {
			t.Errorf("%s in the read-only canvas = %v, want %v", name, focused[name], !acts)
		}
	}
}

func TestBrowserPluginIsAbsentWithoutABrowserOrWhenSwitchedOff(t *testing.T) {
	env := browserEnv{DataDir: t.TempDir()}

	t.Setenv("AGENTGO_BROWSER_PATH", t.TempDir()+"/no-such-browser.exe")
	if _, ok := browsercontrolPlugin(env); ok {
		t.Fatal("offered browser tools with no browser to drive")
	}

	t.Setenv("AGENTGO_BROWSER_PATH", anyExecutable(t))
	if _, ok := browsercontrolPlugin(env); !ok {
		t.Fatal("not offered although a browser path exists")
	}
	if _, ok := browsercontrolPlugin(browserEnv{}); ok {
		t.Fatal("offered without a data directory to keep the profile in")
	}
	t.Setenv("AGENTGO_BROWSER_CONTROL", "0")
	if _, ok := browsercontrolPlugin(env); ok {
		t.Fatal("AGENTGO_BROWSER_CONTROL=0 did not switch the tools off")
	}
}
