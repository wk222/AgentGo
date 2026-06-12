package agent

import "testing"

func TestModeAppMatrixAllowsRunWorkflow(t *testing.T) {
	sm := SessionMode{Profile: ModeAppMatrix, Canvas: CanvasBalanced}
	allow := sm.StaticToolAllowlist()
	if !allow["run_workflow"] {
		t.Fatal("app_matrix should allow run_workflow")
	}
}

func TestModeFocusedBlocksBashDynamic(t *testing.T) {
	sm := SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused}
	if sm.AllowsDynamicTool("my_execute_bash_tool") {
		t.Fatal("focused canvas should block bash-like dynamic tools")
	}
}
