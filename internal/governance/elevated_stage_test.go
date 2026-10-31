package governance

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

func elevatedCtx(mode, command string) ToolPolicyContext {
	args := `{"command":"` + strings.ReplaceAll(command, `\`, `\\`) + `","reason":"安装驱动需要管理员权限"}`
	return ToolPolicyContext{ToolName: ElevatedTool, Arguments: args, Control: ControlPolicyFromMode(mode)}
}

func TestElevatedStageShowsWhatGetsAdministratorRights(t *testing.T) {
	d := (ElevatedStage{}).Evaluate(elevatedCtx("balanced", `pnputil /add-driver C:\drv\a.inf /install`))
	if d == nil || !d.Allowed || !d.RequiresApproval || d.Risk != RiskCritical {
		t.Fatalf("an elevated command must be held for approval at critical risk: %+v", d)
	}
	for _, want := range []string{"pnputil /add-driver", "安装驱动需要管理员权限", "UAC"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("the approver must see %q: %q", want, d.Reason)
		}
	}
	if (ElevatedStage{}).Evaluate(ToolPolicyContext{ToolName: "execute_bash", Arguments: `{"command":"dir"}`}) != nil {
		t.Error("the stage is only about run_elevated")
	}
}

func TestElevatedStageRefusesCommandsNobodyShouldElevate(t *testing.T) {
	for _, c := range []string{
		"diskpart /s clean.txt", "format D: /q", "BCDEDIT /set safeboot minimal", "vssadmin  delete shadows /all",
		"net user hacker P@ss1 /add", "net localgroup administrators hacker /add", `rd /s /q C:\`,
		"powershell Set-MpPreference -DisableRealtimeMonitoring $true", "netsh advfirewall set allprofiles state off",
	} {
		if d := (ElevatedStage{}).Evaluate(elevatedCtx("open", c)); d == nil || d.Allowed {
			t.Errorf("%q must never run as administrator: %+v", c, d)
		}
	}
	for _, c := range []string{"sc stop Spooler", "netsh winsock reset", "ipconfig /flushdns", "reg add HKLM\\SOFTWARE\\X /v a /d 1 /f"} {
		if d := (ElevatedStage{}).Evaluate(elevatedCtx("balanced", c)); d == nil || !d.Allowed {
			t.Errorf("%q is an ordinary admin task: %+v", c, d)
		}
	}
}

// Whatever else is waived, elevation is not: not in open mode, not in a session
// that auto-approves its other tools.
func TestElevationIsNeverWaived(t *testing.T) {
	for _, mode := range []string{"strict", "balanced", "open"} {
		p := BuildPolicy(mode, t.TempDir())
		if !p.RequiresApprovalFor(ElevatedTool, ToolControlDecision{}) {
			t.Errorf("%s: run_elevated must always ask", mode)
		}
		p.SessionAutoApprove = true
		if !p.RequiresApprovalFor(ElevatedTool, ToolControlDecision{}) {
			t.Errorf("%s: an auto-approving session must still ask for run_elevated", mode)
		}
		if p.RequiresApprovalFor("execute_bash", ToolControlDecision{}) {
			t.Errorf("%s: auto-approve is meant to keep working for other tools", mode)
		}
		if p.EffectiveRisk(ElevatedTool, ToolControlDecision{}) != RiskCritical {
			t.Errorf("%s: run_elevated is critical risk", mode)
		}
	}
}

func TestElevatedCallWaitsForTheUserEvenInAutoApproveSessions(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	policy := BuildPolicy("open", t.TempDir())
	policy.SessionAutoApprove = true
	mw := NewGovernanceMiddleware(queue, policy)
	ran := false
	wrapped, _ := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		ran = true
		return "ok", nil
	}, &adk.ToolContext{Name: ElevatedTool})
	ctx := context.Background()
	if _, err := wrapped(ctx, `{"command":"sc stop Spooler","reason":"停止打印服务"}`); err == nil || ran {
		t.Fatalf("the call must pause for approval (ran=%v err=%v)", ran, err)
	}
	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 1 || !strings.Contains(pending[0].Prompt, "sc stop Spooler") || !strings.Contains(pending[0].Prompt, "UAC") {
		t.Fatalf("the pending request must tell the user what runs as administrator: %+v", pending)
	}
	if _, err := wrapped(ctx, `{"command":"diskpart","reason":"x"}`); err == nil || ran {
		t.Fatal("a refused command must not run")
	}
}
