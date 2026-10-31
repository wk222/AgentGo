package governance

import (
	"encoding/json"
	"strings"
)

// ElevatedTool is the tool that runs one command with Windows administrator
// rights. It is the only tool whose approval is never waived: not by a
// session's auto-approve, not by the open control mode.
const ElevatedTool = "run_elevated"

// elevatedDenied are commands that are never worth running as administrator,
// whatever the reason: they destroy disks and recovery data, or hand out
// permanent access. The user can still do them by hand.
var elevatedDenied = []string{
	"diskpart", "bcdedit", "vssadmin delete", "wbadmin delete", "cipher /w",
	"format ", "reg delete hk", "sc delete", "net user ", "net localgroup administrators",
	"takeown /f c:\\ ", "icacls c:\\ ", "rd /s /q c:\\", "rmdir /s /q c:\\", "del /s /q c:\\", "del /f /s /q c:\\",
	"set-mppreference", "disableantispyware", "netsh advfirewall set allprofiles state off",
}

// ElevatedStage tells the approver what is about to get administrator rights
// and refuses the commands above.
type ElevatedStage struct{}

func (ElevatedStage) Name() string { return "elevated" }

func (ElevatedStage) Evaluate(ctx ToolPolicyContext) *ToolControlDecision {
	if ctx.ToolName != ElevatedTool {
		return nil
	}
	var args struct {
		Command string `json:"command"`
		Reason  string `json:"reason"`
	}
	_ = json.Unmarshal([]byte(ctx.Arguments), &args)
	command := strings.TrimSpace(args.Command)
	lower := strings.ToLower(strings.Join(strings.Fields(command), " "))
	for _, bad := range elevatedDenied {
		if strings.Contains(lower, bad) {
			return &ToolControlDecision{
				Allowed: false, Risk: RiskCritical,
				Reason:      "该命令不允许以管理员身份运行（" + strings.TrimSpace(bad) + "）；请用户在需要时自己手动执行",
				ControlTags: []string{"elevated", "elevated-denied"},
			}
		}
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		reason = "（未说明）"
	}
	return &ToolControlDecision{
		Allowed: true, Risk: RiskCritical, RequiresApproval: true,
		Reason: "以管理员身份运行: " + command + "\n原因: " + reason +
			"\n批准后 Windows 可能弹出 UAC 窗口，需要在本机确认；这条命令将拥有完整的管理员权限，只批准你看得懂的命令。",
		ControlTags: []string{"elevated", "needs-approval"},
	}
}
