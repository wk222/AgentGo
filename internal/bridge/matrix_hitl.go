package bridge

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
)

func matrixTraceContext(ctx context.Context, sessionID string) context.Context {
	app := application.Get()
	if app == nil {
		return ctx
	}
	return agent.WithTraceEmitter(ctx, agent.TraceEmitterFromWails(func(p map[string]any) {
		p["session_id"] = sessionID
		p["matrix"] = true
		app.Event.Emit("agent:trace", p)
	}))
}

func (rt *Runtime) matrixRegisterPause(res *agent.RunResult, ev capability.Event, prompt string) {
	if res == nil || res.PendingApproval == nil {
		return
	}
	p := res.PendingApproval
	approvalID := p.ApprovalID
	if approvalID == "" {
		approvalID = p.InterruptID
	}
	interruptID := p.InterruptID
	if interruptID == "" {
		interruptID = approvalID
	}
	rt.pending.Set(pendingRun{
		SessionID:   agent.MatrixCoordinatorSession,
		UserText:    prompt,
		ToolName:    p.ToolName,
		Arguments:   p.Arguments,
		ApprovalID:  approvalID,
		InterruptID: interruptID,
		ResumeKind:  "matrix",
		MatrixEvent: ev.Type + ":" + ev.Source,
	})
	app := application.Get()
	if app == nil {
		return
	}
	payload := map[string]any{
		"approval_id":  approvalID,
		"interrupt_id": interruptID,
		"tool_name":    p.ToolName,
		"arguments":    p.Arguments,
		"event_type":   ev.Type,
		"source":       ev.Source,
		"session_id":   ev.SessionID,
	}
	app.Event.Emit("approval:pending", payload)
	app.Event.Emit("matrix:paused", payload)
}
