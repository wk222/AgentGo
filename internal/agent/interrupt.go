package agent

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk"

	"agentgo/internal/governance"
	"agentgo/internal/interactive"
)

func parseInterruptPause(info any, interruptID string) *PendingApproval {
	switch v := info.(type) {
	case interactive.QuestionPayload:
		return &PendingApproval{
			InterruptID: interruptID,
			ApprovalID:  interruptID,
			ToolName:    "ask_user",
			Arguments:   v.Prompt,
			Question:    &v,
		}
	case governance.ToolApprovalPause:
		return &PendingApproval{
			InterruptID: interruptID,
			ApprovalID:  v.ApprovalID,
			ToolName:    v.ToolName,
			Arguments:   v.Arguments,
		}
	case map[string]any:
		if tool, _ := v["tool"].(string); tool == "ask_user" {
			b, _ := json.Marshal(v)
			var q interactive.QuestionPayload
			_ = json.Unmarshal(b, &q)
			return &PendingApproval{
				InterruptID: interruptID, ApprovalID: interruptID,
				ToolName: "ask_user", Arguments: q.Prompt, Question: &q,
			}
		}
		approvalID := interruptID
		if id, ok := v["approval_id"].(string); ok && id != "" {
			approvalID = id
		}
		toolName := fmt.Sprintf("%v", v["tool"])
		args := fmt.Sprintf("%v", v["arguments"])
		if args == "<nil>" {
			args = fmt.Sprintf("%v", v)
		}
		return &PendingApproval{
			InterruptID: interruptID,
			ApprovalID:  approvalID,
			ToolName:    toolName,
			Arguments:   args,
		}
	default:
		return &PendingApproval{
			InterruptID: interruptID,
			ApprovalID:  interruptID,
			ToolName:    "execute_bash",
			Arguments:   fmt.Sprintf("%v", info),
		}
	}
}

// pickInterruptPause selects the best pause from a composite interrupt chain (Eino AgentTool / HITL).
func pickInterruptPause(contexts []*adk.InterruptCtx) (interruptID string, pause *PendingApproval) {
	if len(contexts) == 0 {
		return "", nil
	}
	var fallback *adk.InterruptCtx
	for _, ic := range contexts {
		if ic == nil {
			continue
		}
		if fallback == nil {
			fallback = ic
		}
		if ic.IsRootCause {
			fallback = ic
			break
		}
	}
	if fallback == nil {
		return "", nil
	}
	return fallback.ID, parseInterruptPause(fallback.Info, fallback.ID)
}
