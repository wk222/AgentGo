package agent

import (
	"testing"

	"agentgo/internal/tools"
)

func TestPluginStaticToolsRespectCanvas(t *testing.T) {
	defer tools.RegisterStaticTool("pt_read", false)()
	defer tools.RegisterStaticTool("pt_write", true)()

	for _, prof := range []ModeProfile{ModeAssistant, ModeAppMatrix} {
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
