package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/governance"
)

// ArmorBreakToolName is the pseudo tool name of the "start the rollback?"
// decision. It rides the ordinary approval queue, so the desktop card, the web
// UI, the CLI prompt (and -yolo auto-approve), the pending-run store and the
// audit all handle it without a second mechanism.
const ArmorBreakToolName = governance.ArmorBreakToolName

// armorConfirmPrompt is shown on the approval card; the text after the
// "Review required." marker is what the UI displays.
const armorConfirmPrompt = "Review required.\n" +
	"模型这一轮没有调用任何工具，回复看起来是拒绝或请示。是否开始越狱？\n" +
	"同意：把这一轮从模型看到的上下文里回退，补一段确认并注入会话记忆，再续跑一次；磁盘上的会话记录不变。\n" +
	"拒绝：保持这一轮的回复，不做任何改动。"

type armorConfirmArgs struct {
	Hesitant string `json:"hesitant"`
}

// armorConfirmRequired: the reverse profile asks first. The local red-team
// harness (env-driven automation) keeps the silent continuation, as does
// AGENTGO_ARMOR_CONFIRM=0. Without an approval queue there is nobody to ask.
func (r *Runner) armorConfirmRequired(ctx context.Context) bool {
	if r == nil || r.queue == nil || !config.ArmorConfirm.On() {
		return false
	}
	return SessionModeFromContext(ctx).Profile == ModeReverse
}

// requestArmorConfirm records the decision as a pending approval and returns
// the pause for the caller to surface. nil means it could not be recorded, in
// which case the caller falls back to the automatic continuation.
func (r *Runner) requestArmorConfirm(ctx context.Context, sessionID, hesitant string, strategy ArmorStrategy) *PendingApproval {
	args, err := json.Marshal(armorConfirmArgs{Hesitant: strings.TrimSpace(hesitant)})
	if err != nil {
		return nil
	}
	sid := strings.TrimPrefix(sessionID, "agentic:")
	req := governance.NewApprovalRequest(string(governance.KindToolApproval), "agent_runtime",
		"Start armor-break continuation", "[LOW RISK] Armor-break confirmation: "+ArmorBreakToolName+". "+armorConfirmPrompt)
	req.Metadata = map[string]interface{}{
		"session_id": sid,
		"tool_name":  ArmorBreakToolName,
		"arguments":  string(args),
		"risk":       "LOW",
		"strategy":   string(strategy),
	}
	if err := r.queue.CreateRequest(ctx, req); err != nil {
		return nil
	}
	_ = r.queue.RecordAudit(ctx, governance.AuditEntry{
		Channel: "governance", SessionID: sid, Action: "armor_break_confirm_requested",
		ToolName: ArmorBreakToolName, Result: "pending", RiskLevel: "LOW",
		Arguments:   armorAuditMetaFor(hesitant, strategy),
		Explanation: "破甲前先问：模型这一轮无工具调用且回复为拒绝/请示，等待用户决定是否回退并续跑；决定前没有改动任何模型视图。",
	})
	return &PendingApproval{ApprovalID: req.ID, ToolName: ArmorBreakToolName, Arguments: string(args)}
}

// ContinueArmorBreak runs the rollback-and-inject continuation after the user
// said yes. arguments is the pause's JSON (the hesitant reply to roll back).
// The result carries only what the continuation produced; the hesitant reply
// was already shown and stored in the turn that asked.
func (r *Runner) ContinueArmorBreak(ctx context.Context, cfg LLMSettings, sessionID, arguments string, emit func(string)) (*RunResult, error) {
	var a armorConfirmArgs
	if err := json.Unmarshal([]byte(arguments), &a); err != nil || strings.TrimSpace(a.Hesitant) == "" {
		return nil, errors.New("破甲续跑缺少要回退的那一轮回复")
	}
	if armorStrategyFromContext(ctx) == ArmorStrategyNone {
		return nil, errors.New("破甲已被配置关闭，不能续跑")
	}
	res, err := r.armorContinue(ctx, cfg, sessionID, a.Hesitant, &RunResult{}, emit)
	if err != nil {
		return nil, err
	}
	return res, nil
}
