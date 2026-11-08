package governance

import "testing"

func TestControlStrictBlocksDynamicTools(t *testing.T) {
	p := ControlPolicyFromMode("strict")
	d := p.EvaluateToolCall("my_dyn_tool", true)
	if d.Allowed {
		t.Fatal("strict should block dynamic tools")
	}
}

func TestControlOpenSkipsApprovalExceptBash(t *testing.T) {
	p := ControlPolicyFromMode("open")
	d := p.EvaluateToolCall("create_tool", false)
	if d.RequiresApproval {
		t.Fatal("open should not require approval for create_tool")
	}
	d2 := p.EvaluateToolCall("execute_bash", false)
	if !d2.RequiresApproval {
		t.Fatal("execute_bash should always require approval")
	}
}

func TestBuildPolicyStrictBlocksMutation(t *testing.T) {
	pol := BuildPolicy("strict", t.TempDir())
	if !pol.BlockedTools["create_tool"] {
		t.Fatal("strict should block create_tool")
	}
}

func TestBalancedRequiresApprovalForWorkspaceMutation(t *testing.T) {
	p := ControlPolicyFromMode("balanced")
	for _, name := range []string{"replace_workspace_text", "create_workspace_file"} {
		d := p.EvaluateToolCall(name, false)
		if !d.Allowed || !d.RequiresApproval || d.Risk != RiskMedium {
			t.Fatalf("unexpected decision for %s: %+v", name, d)
		}
	}
}

func TestStrictAllowsReviewedWorkspaceMutationButBlocksToolMutation(t *testing.T) {
	p := ControlPolicyFromMode("strict")
	fileEdit := p.EvaluateToolCall("replace_workspace_text", false)
	if !fileEdit.Allowed || !fileEdit.RequiresApproval {
		t.Fatalf("strict should permit workspace edits only with approval: %+v", fileEdit)
	}
	toolEdit := p.EvaluateToolCall("create_tool", false)
	if toolEdit.Allowed {
		t.Fatalf("strict should still block tool mutation: %+v", toolEdit)
	}
}

// A node can carry Python and its contract tests run it, so publishing one is held to
// the same bar as creating a tool.
func TestPublishingAWorkflowNodeIsGovernedLikeCreatingATool(t *testing.T) {
	if d := ControlPolicyFromMode("strict").EvaluateToolCall("define_workflow_node", false); d.Allowed {
		t.Fatalf("strict must block publishing nodes: %+v", d)
	}
	if d := ControlPolicyFromMode("balanced").EvaluateToolCall("define_workflow_node", false); !d.Allowed || !d.RequiresApproval {
		t.Fatalf("balanced must ask before publishing a node: %+v", d)
	}
	if d := ControlPolicyFromMode("open").EvaluateToolCall("define_workflow_node", false); d.RequiresApproval {
		t.Fatalf("open waives approval, as it does for create_tool: %+v", d)
	}
	if !BuildPolicy("strict", t.TempDir()).BlockedTools["define_workflow_node"] {
		t.Fatal("strict policy must list define_workflow_node as blocked")
	}
	if got := BuildPolicy("balanced", t.TempDir()).ToolRiskLevels["define_workflow_node"]; got != RiskHigh {
		t.Fatalf("risk = %q, want high", got)
	}
}

func TestRunBatchScriptAlwaysRequiresApproval(t *testing.T) {
	for _, mode := range []string{"strict", "balanced", "open"} {
		d := ControlPolicyFromMode(mode).EvaluateToolCall("run_batch_script", false)
		if !d.RequiresApproval || d.Risk != RiskHigh {
			t.Fatalf("mode %s should protect host script execution: %+v", mode, d)
		}
	}
}

func TestPathPolicyBlocksTraversal(t *testing.T) {
	root := t.TempDir()
	pipe := BuildDefaultToolPolicyPipeline()
	dec := pipe.Evaluate(ToolPolicyContext{
		ToolName:    "read_workspace",
		Arguments:   `{"path":"../../etc/passwd"}`,
		AllowedRoot: root,
		Control:     ControlPolicyFromMode("balanced"),
	})
	if dec.Allowed {
		t.Fatal("expected path traversal block")
	}
}

func TestRateLimitStage(t *testing.T) {
	pipe := NewToolPolicyPipeline(RateLimitStage{})
	ctrl := ControlPolicyFromMode("balanced")
	ctrl.MaxCallsPerTool = 2
	ctx := ToolPolicyContext{
		ToolName: "echo", SessionID: "s1", Control: ctrl, RecentCalls: 2,
	}
	dec := pipe.Evaluate(ctx)
	if dec.Allowed {
		t.Fatal("expected rate limit deny")
	}
}
