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

func TestAssistantBalancedHasCodingLoopTools(t *testing.T) {
	allow := (SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced}).StaticToolAllowlist()
	for _, name := range []string{
		"read_workspace_file", "search_workspace", "replace_workspace_text",
		"create_workspace_file", "get_workspace_changes", "execute_bash", "run_batch_script",
	} {
		if !allow[name] {
			t.Fatalf("balanced assistant should allow %s", name)
		}
	}
}

func TestAssistantFocusedIsReadOnlyWorkspaceMode(t *testing.T) {
	allow := (SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused}).StaticToolAllowlist()
	for _, name := range []string{"list_workspace_dir", "read_workspace_file", "search_workspace", "get_workspace_changes"} {
		if !allow[name] {
			t.Fatalf("focused assistant should allow read tool %s", name)
		}
	}
	for _, name := range []string{"replace_workspace_text", "create_workspace_file", "execute_bash", "run_batch_script"} {
		if allow[name] {
			t.Fatalf("focused assistant should block action tool %s", name)
		}
	}
}
