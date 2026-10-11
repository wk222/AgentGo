package bridge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

func TestOfficedocPluginContributesGovernedTools(t *testing.T) {
	ws := t.TempDir()
	s, reg, bus := newCodetoolsService(t, ws)
	if pluginState(t, s, "officedoc") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	for _, n := range []string{"create_docx", "create_xlsx"} {
		registryTool(t, reg, n)
		if st, ok := grantStatus(bus, "tool", "officedoc", n); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", n, st, ok)
		}
		// One declaration drives visibility, file-changing and risk.
		if !tools.IsStaticTool(n) {
			t.Errorf("%s must be always visible", n)
		}
		if tr, ok := governance.ToolTraitsOf(n); !ok || !tr.Mutating || tr.Source != "officedoc" {
			t.Errorf("%s traits = %+v ok=%v", n, tr, ok)
		}
		if got := governance.StaticRisk(n); got != governance.RiskMedium {
			t.Errorf("%s risk = %s", n, got)
		}
		for _, g := range bus.List("tool") {
			if g.Name == n && g.RiskLevel != "medium" {
				t.Errorf("bus risk of %s = %q", n, g.RiskLevel)
			}
		}
		// Reachable in the normal modes, hidden only in the read-only canvas.
		open := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()
		if !open[n] {
			t.Errorf("%s not offered in the assistant balanced mode", n)
		}
		focused := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasFocused}.StaticToolAllowlist()
		if focused[n] {
			t.Errorf("%s offered in the read-only canvas", n)
		}
	}

	out := invoke(t, registryTool(t, reg, "create_docx"), map[string]any{
		"relative_path": "notes/minutes", "markdown": "# 会议纪要\n\n- 事项一\n- 事项二",
	})
	if !strings.Contains(out, `"path":"notes/minutes.docx"`) {
		t.Fatalf("output = %s", out)
	}
	if st, err := os.Stat(filepath.Join(ws, "notes", "minutes.docx")); err != nil || st.Size() == 0 {
		t.Fatalf("file not written inside the workspace: %v", err)
	}

	invoke(t, registryTool(t, reg, "create_xlsx"), map[string]any{
		"relative_path": "budget.xlsx",
		"sheets":        []map[string]any{{"name": "Q1", "rows": [][]any{{"项", "额"}, {"a", 1}}}},
	})
	f, err := excelize.OpenFile(filepath.Join(ws, "budget.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// The workspace jail applies to the real resolver too.
	// ("/x" has no drive on Windows and resolves inside the workspace, which is safe.)
	for _, bad := range []string{"../escape.docx", "a/../../escape.docx", `C:\abs.docx`} {
		if _, err := registryTool(t, reg, "create_docx").InvokableRun(t.Context(),
			`{"relative_path":`+quote(bad)+`,"markdown":"x"}`); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape.docx")); err == nil {
		t.Error("a file escaped the workspace")
	}

	// Stopping the plugin withdraws the tools, the declarations and the grants.
	s.StopPlugin("officedoc")
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(t.Context()); info.Name == "create_docx" || info.Name == "create_xlsx" {
			t.Fatalf("%s survived plugin stop", info.Name)
		}
	}
	if tools.IsStaticTool("create_docx") {
		t.Error("static mark survived plugin stop")
	}
	if _, ok := governance.ToolTraitsOf("create_docx"); ok {
		t.Error("traits survived plugin stop")
	}
	if st, _ := grantStatus(bus, "tool", "officedoc", "create_docx"); st != capability.StatusRetired {
		t.Errorf("grant after stop = %q", st)
	}
}

func TestOfficedocPluginFollowsTheWorkspaceWithoutReload(t *testing.T) {
	first := t.TempDir()
	s, reg, _ := newCodetoolsService(t, first)
	if pluginState(t, s, "officedoc") != plugin.StateRunning {
		t.Fatal("officedoc not running")
	}
	second := t.TempDir()
	s.rt.mu.Lock()
	s.rt.workspace = second
	s.rt.mu.Unlock()
	invoke(t, registryTool(t, reg, "create_docx"), map[string]any{"relative_path": "x.docx", "markdown": "# x"})
	if _, err := os.Stat(filepath.Join(second, "x.docx")); err != nil {
		t.Fatalf("not written to the current workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, "x.docx")); err == nil {
		t.Fatal("written to the previous workspace")
	}
}

func quote(s string) string { return strconv.Quote(s) } // valid JSON for these inputs

func TestOfficedocPluginCanBeDisabled(t *testing.T) {
	t.Setenv("AGENTGO_OFFICEDOC", "0")
	s, reg, _ := newCodetoolsService(t, t.TempDir())
	for _, st := range s.ListPlugins() {
		if st.Name == "officedoc" {
			t.Fatalf("plugin must be absent when disabled: %+v", st)
		}
	}
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(t.Context()); info.Name == "create_docx" {
			t.Fatal("tool registered although the plugin is disabled")
		}
	}
}
