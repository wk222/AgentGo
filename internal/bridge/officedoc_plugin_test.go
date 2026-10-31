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

// officedocTools is every tool the plugin contributes; the readers change
// nothing and so are governed more lightly than the writers.
var officedocTools = []string{"create_docx", "create_xlsx", "read_xlsx", "analyze_xlsx", "edit_xlsx", "create_pptx", "inspect_pptx"}

func TestOfficedocPluginContributesGovernedTools(t *testing.T) {
	ws := t.TempDir()
	s, reg, bus := newCodetoolsService(t, ws)
	if pluginState(t, s, "officedoc") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	for _, n := range officedocTools {
		readOnly := n == "read_xlsx" || n == "analyze_xlsx" || n == "inspect_pptx"
		registryTool(t, reg, n)
		if st, ok := grantStatus(bus, "tool", "officedoc", n); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", n, st, ok)
		}
		// One declaration drives visibility, file-changing and risk.
		if !tools.IsStaticTool(n) {
			t.Errorf("%s must be always visible", n)
		}
		tr, ok := governance.ToolTraitsOf(n)
		if !ok || tr.Mutating == readOnly || tr.Source != "officedoc" {
			t.Errorf("%s traits = %+v ok=%v (read-only=%v)", n, tr, ok, readOnly)
		}
		wantRisk := governance.RiskMedium
		if readOnly {
			wantRisk = governance.RiskLow
		}
		if got := governance.StaticRisk(n); got != wantRisk {
			t.Errorf("%s risk = %s, want %s", n, got, wantRisk)
		}
		for _, g := range bus.List("tool") {
			if want := strings.ToLower(string(wantRisk)); g.Name == n && g.RiskLevel != want {
				t.Errorf("bus risk of %s = %q, want %q", n, g.RiskLevel, want)
			}
		}
		// Reachable in the normal modes; only the writers are hidden in the
		// read-only canvas.
		open := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()
		if !open[n] {
			t.Errorf("%s not offered in the assistant balanced mode", n)
		}
		focused := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasFocused}.StaticToolAllowlist()
		if focused[n] != readOnly {
			t.Errorf("%s in the read-only canvas = %v, want %v", n, focused[n], readOnly)
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

	// Read, edit and read back through the registry, then a deck and its check.
	read := func() string {
		return invoke(t, registryTool(t, reg, "read_xlsx"), map[string]any{"relative_path": "budget.xlsx", "sheet": "Q1"})
	}
	if out := read(); !strings.Contains(out, `"Q1"`) || !strings.Contains(out, "额") {
		t.Fatalf("read_xlsx = %s", out)
	}
	invoke(t, registryTool(t, reg, "edit_xlsx"), map[string]any{
		"relative_path": "budget.xlsx", "no_backup": true,
		"operations": []map[string]any{
			{"op": "set_cell", "sheet": "Q1", "cell": "B3", "value": 41},
			{"op": "set_range", "sheet": "Q1", "cell": "C1", "rows": [][]any{{"合计"}, {"=B2+B3"}}},
		},
	})
	if out := read(); !strings.Contains(out, "合计") || !strings.Contains(out, "42") {
		t.Fatalf("edit_xlsx not visible to read_xlsx: %s", out)
	}
	if out := invoke(t, registryTool(t, reg, "analyze_xlsx"), map[string]any{
		"relative_path": "budget.xlsx", "queries": []map[string]any{{"name": "s", "metrics": []map[string]any{{"col": "额", "fn": "sum"}}}},
	}); !strings.Contains(out, `"rows":[[42]]`) {
		t.Fatalf("analyze_xlsx = %s", out)
	}
	invoke(t, registryTool(t, reg, "create_pptx"), map[string]any{
		"relative_path": "decks/q1", "title": "季度汇报",
		"slides": []map[string]any{{"layout": "bullets", "title": "结论先行", "bullets": []string{"要点一", "要点二"}}},
	})
	if out := invoke(t, registryTool(t, reg, "inspect_pptx"), map[string]any{"relative_path": "decks/q1.pptx"}); !strings.Contains(out, "结论先行") || strings.Contains(out, `"level":"warn"`) {
		t.Fatalf("inspect_pptx = %s", out)
	}
	// Another workspace file stays out of reach of the readers as well.
	for _, name := range []string{"read_xlsx", "analyze_xlsx", "inspect_pptx"} {
		if _, err := registryTool(t, reg, name).InvokableRun(t.Context(), `{"relative_path":"../escape.xlsx"}`); err == nil {
			t.Errorf("%s accepted a path outside the workspace", name)
		}
	}

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
		info, _ := bt.Info(t.Context())
		for _, n := range officedocTools {
			if info.Name == n {
				t.Fatalf("%s survived plugin stop", n)
			}
		}
	}
	for _, n := range officedocTools {
		if tools.IsStaticTool(n) {
			t.Errorf("static mark of %s survived plugin stop", n)
		}
		if _, ok := governance.ToolTraitsOf(n); ok {
			t.Errorf("traits of %s survived plugin stop", n)
		}
		if st, _ := grantStatus(bus, "tool", "officedoc", n); st != capability.StatusRetired {
			t.Errorf("grant of %s after stop = %q", n, st)
		}
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
