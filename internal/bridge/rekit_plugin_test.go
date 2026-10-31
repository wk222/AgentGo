package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/rekit"
	"agentgo/internal/tools"
)

var allRekitTools = []string{
	"re_env",
	"re_triage",
	"re_apk_inspect",
	"re_strings",
	"re_xor_probe",
	"re_har_summary",
	"re_har_entry",
	"re_evidence_add",
	"re_evidence_list",
	"re_report",
	"re_tool_run",
	"re_ghidra_export",
	"re_gate",
}

func TestRekitPluginContributesGovernedTools(t *testing.T) {
	ws := t.TempDir()
	s, reg, bus := newCodetoolsService(t, ws)
	if pluginState(t, s, "rekit") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}

	revBalanced := agent.SessionMode{Profile: agent.ModeReverse, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()
	revFocused := agent.SessionMode{Profile: agent.ModeReverse, Canvas: agent.CanvasFocused}.StaticToolAllowlist()
	stdBalanced := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()

	for _, n := range allRekitTools {
		registryTool(t, reg, n)
		if st, ok := grantStatus(bus, "tool", "rekit", n); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", n, st, ok)
		}
		if !tools.IsStaticTool(n) {
			t.Errorf("%s must be declared static", n)
		}
		mut := rekit.IsMutating(n)
		wantRisk := rekit.Risk(n)
		if tr, ok := governance.ToolTraitsOf(n); !ok || tr.Mutating != mut || tr.Source != "rekit" {
			t.Errorf("%s traits = %+v ok=%v", n, tr, ok)
		}
		if got := governance.StaticRisk(n); got != wantRisk {
			t.Errorf("%s risk = %s, want %s", n, got, wantRisk)
		}
		if !revBalanced[n] {
			t.Errorf("%s must be in reverse/balanced allowlist", n)
		}
		if revFocused[n] != !mut {
			t.Errorf("%s in reverse/focused = %v, want %v", n, revFocused[n], !mut)
		}
		if stdBalanced[n] {
			t.Errorf("%s must not bloat assistant/balanced static allowlist", n)
		}
	}

	// End-to-end invocation through the Eino InvokableTool adapter.
	sample := filepath.Join(ws, "masked.bin")
	raw := []byte{'M' ^ 0x5a, 'Z' ^ 0x5a, 0x90 ^ 0x5a, 0x00 ^ 0x5a, 'h', 'e', 'l', 'l', 'o'}
	if err := os.WriteFile(sample, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	triageOut := invoke(t, registryTool(t, reg, "re_triage"), map[string]any{"path": sample})
	if !strings.Contains(triageOut, `"tool": "re_triage"`) || !strings.Contains(triageOut, `"ok": true`) {
		t.Fatalf("re_triage output = %s", triageOut)
	}

	addOut := invoke(t, registryTool(t, reg, "re_evidence_add"), map[string]any{
		"case":   "unit",
		"kind":   "observation",
		"claim":  "sample head is 9 bytes",
		"source": "re_triage masked.bin",
	})
	if !strings.Contains(addOut, `"id": "ev-0001"`) {
		t.Fatalf("re_evidence_add output = %s", addOut)
	}
	repOut := invoke(t, registryTool(t, reg, "re_report"), map[string]any{"case": "unit"})
	if !strings.Contains(repOut, "REPORT.md") {
		t.Fatalf("re_report output = %s", repOut)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agentgo-re", "cases", "unit", "REPORT.md")); err != nil {
		t.Fatalf("REPORT.md not written under workspace .agentgo-re: %v", err)
	}

	// Stopping the plugin withdraws all 11 tools, declarations and grants.
	s.StopPlugin("rekit")
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(t.Context()); strings.HasPrefix(info.Name, "re_") {
			t.Fatalf("%s survived plugin stop", info.Name)
		}
	}
	if tools.IsStaticTool("re_triage") {
		t.Error("static mark survived plugin stop")
	}
	if _, ok := governance.ToolTraitsOf("re_xor_probe"); ok {
		t.Error("traits survived plugin stop")
	}
	if st, _ := grantStatus(bus, "tool", "rekit", "re_triage"); st != capability.StatusRetired {
		t.Errorf("grant after stop = %q", st)
	}
}

func TestRekitPluginFollowsTheWorkspaceWithoutReload(t *testing.T) {
	first := t.TempDir()
	s, reg, _ := newCodetoolsService(t, first)
	if pluginState(t, s, "rekit") != plugin.StateRunning {
		t.Fatal("rekit not running")
	}
	second := t.TempDir()
	s.rt.mu.Lock()
	s.rt.workspace = second
	s.rt.mu.Unlock()
	invoke(t, registryTool(t, reg, "re_evidence_add"), map[string]any{
		"case":   "moved",
		"kind":   "observation",
		"claim":  "workspace switched",
		"source": "unit test",
	})
	if _, err := os.Stat(filepath.Join(second, ".agentgo-re", "cases", "moved", "evidence.jsonl")); err != nil {
		t.Fatalf("not written to the current workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, ".agentgo-re", "cases", "moved", "evidence.jsonl")); err == nil {
		t.Fatal("written to the previous workspace")
	}
}

func TestRekitPluginCanBeDisabled(t *testing.T) {
	t.Setenv("AGENTGO_REKIT", "0")
	s, reg, _ := newCodetoolsService(t, t.TempDir())
	for _, st := range s.ListPlugins() {
		if st.Name == "rekit" {
			t.Fatalf("plugin must be absent when disabled: %+v", st)
		}
	}
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(t.Context()); strings.HasPrefix(info.Name, "re_") {
			t.Fatalf("tool %s registered although rekit is disabled", info.Name)
		}
	}
}
