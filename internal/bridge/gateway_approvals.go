package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"agentgo/internal/engine"
	"agentgo/internal/gateway"
)

var _ gateway.ApprovalResolver = (*RuntimeGateway)(nil)

// ResolveApproval implements gateway.ApprovalResolver: it settles the approval
// and streams the run the decision unblocks, so a headless client (the
// evaluation harness, scripts) can drive a run through its approvals.
func (g *RuntimeGateway) ResolveApproval(ctx context.Context, approvalID string, approve bool, emit func(event string, data []byte) error) (*gateway.ChatResult, error) {
	svc := &AppService{rt: g.rt}
	var tools atomic.Int64
	var streamed strings.Builder
	tap := engine.SinkFunc(func(ev engine.Event) {
		switch ev.Type {
		case engine.EventToken:
			if d, _ := ev.Payload["delta"].(string); d != "" {
				streamed.WriteString(d)
				_ = emit("chunk", mustJSON(map[string]string{"delta": d, "session_id": ev.SessionID}))
			}
		case engine.EventToolCall:
			tools.Add(1)
		}
	})

	out := svc.ResolveApprovalTap(approvalID, approve, "gateway", tap)
	if ok, _ := out["success"].(bool); !ok {
		msg, _ := out["error"].(string)
		if msg == "" {
			msg = "the decision was not recorded"
		}
		return nil, errors.New(msg)
	}
	if e, _ := out["resume_error"].(string); e != "" {
		return nil, fmt.Errorf("resume: %s", e)
	}

	content := streamed.String()
	if msgs, ok := out["messages"].([]ChatMessageDTO); ok {
		for _, m := range msgs {
			switch {
			case m.Type == "approval" || m.Type == "question":
				_ = emit("interrupt", mustJSON(map[string]any{
					"approval_id": m.ApprovalID,
					"tool_name":   m.ToolName,
				}))
			case m.Role == "assistant" && m.Type == "text" && m.Content != "":
				content = m.Content
			}
		}
	}
	// The resumed segment is not metered like a fresh chat turn, so only the
	// tool count is known.
	return &gateway.ChatResult{Content: content, Usage: &gateway.UsageStats{ToolCalls: int(tools.Load())}}, nil
}
