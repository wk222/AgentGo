package agent

import (
	"testing"

	"agentgo/internal/tools"
)

func TestPluginStaticToolsRespectCanvas(t *testing.T) {
	defer tools.RegisterStaticTool("pt_read", false)()
	defer tools.RegisterStaticTool("pt_write", true)()

	for _, prof := range []ModeProfile{ModeAssistant, ModeAppMatrix, ModeReverse} {
		focused := SessionMode{Profile: prof, Canvas: CanvasFocused}.StaticToolAllowlist()
		if !focused["pt_read"] || focused["pt_write"] {
			t.Fatalf("%s/focused: read=%v write=%v (want true,false)", prof, focused["pt_read"], focused["pt_write"])
		}
		balanced := SessionMode{Profile: prof, Canvas: CanvasBalanced}.StaticToolAllowlist()
		if !balanced["pt_read"] || !balanced["pt_write"] {
			t.Fatalf("%s/balanced must allow both", prof)
		}
	}
	admin := SessionMode{Profile: ModeAdmin}
	if admin.StaticToolAllowlist() != nil {
		t.Fatal("admin keeps nil (all) allowlist")
	}
}

func TestReversePluginToolsScopedToReverseAndAdmin(t *testing.T) {
	defer tools.RegisterStaticTool("re_triage", false)()
	defer tools.RegisterStaticTool("re_xor_probe", true)()

	for _, prof := range []ModeProfile{ModeAssistant, ModeAppMatrix} {
		allow := SessionMode{Profile: prof, Canvas: CanvasBalanced}.StaticToolAllowlist()
		if allow["re_triage"] || allow["re_xor_probe"] {
			t.Fatalf("%s must not include re_* tools in static allowlist", prof)
		}
	}
	revFocused := SessionMode{Profile: ModeReverse, Canvas: CanvasFocused}.StaticToolAllowlist()
	if !revFocused["re_triage"] || revFocused["re_xor_probe"] {
		t.Fatalf("reverse/focused: triage=%v xor=%v (want true,false)", revFocused["re_triage"], revFocused["re_xor_probe"])
	}
	revBalanced := SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced}.StaticToolAllowlist()
	if !revBalanced["re_triage"] || !revBalanced["re_xor_probe"] {
		t.Fatalf("reverse/balanced must allow both read-only and mutating re_* tools")
	}
}
