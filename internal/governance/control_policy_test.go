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
	pipe := BuildDefaultToolPolicyPipeline(BuildPolicy("balanced", root), NewToolCallTracker())
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
	tr := NewToolCallTracker()
	pipe := NewToolPolicyPipeline(RateLimitStage{Tracker: tr})
	ctrl := ControlPolicyFromMode("balanced")
	ctrl.MaxCallsPerTool = 2
	ctx := ToolPolicyContext{
		ToolName: "echo", SessionID: "s1", Control: ctrl,
	}
	tr.Record("s1", "echo")
	tr.Record("s1", "echo")
	dec := pipe.Evaluate(ctx)
	if dec.Allowed {
		t.Fatal("expected rate limit deny")
	}
}
