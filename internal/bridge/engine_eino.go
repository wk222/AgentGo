package bridge

import (
	"context"

	"agentgo/internal/agent"
	"agentgo/internal/engine"
)

// EinoEngine adapts Runtime's native chat operation to engine.AgentEngine.
// The outer Runtime turn boundary owns transcript persistence and completion.
type EinoEngine struct {
	svc chatBackend
}

const EinoEngineID = "eino"

func NewEinoEngine(svc chatBackend) *EinoEngine { return &EinoEngine{svc: svc} }

func (e *EinoEngine) ID() string { return EinoEngineID }

func (e *EinoEngine) Run(ctx context.Context, req engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	// Surface tool calls (id/args/output) and reasoning as engine events, not only text deltas.
	ctx = bindEngineObservers(ctx, em)
	res := e.svc.sendMessageCore(ctx, req.SessionID, req.Input, req.Images, em.Token)
	return sendResultToEngine(res, em), nil
}

func bindEngineObservers(ctx context.Context, em engine.Emitter) context.Context {
	ctx = agent.WithToolObserver(ctx, toolEvents{em})
	ctx = agent.WithReasoningObserver(ctx, reasoningEvents{em})
	return agent.WithTextEmitter(ctx, em.Token)
}

// reasoningEvents adapts the agent's per-run ReasoningObserver to engine events.
type reasoningEvents struct{ em engine.Emitter }

func (r reasoningEvents) ReasoningDelta(delta string) { r.em.Reasoning(delta) }

// toolEvents adapts the agent's per-run ToolObserver to engine events.
type toolEvents struct{ em engine.Emitter }

func (t toolEvents) ToolStarted(id, name, args string) { t.em.ToolCall(id, name, args) }
func (t toolEvents) ToolFinished(id, name, output string, isErr bool) {
	t.em.ToolResult(id, name, output, isErr)
}

// sendResultToEngine converts the legacy result into the unified shape and
// emits approval requests for any pending approval/question messages.
func sendResultToEngine(res SendMessageResult, em engine.Emitter) engine.RunResult {
	out := engine.RunResult{Error: res.Error, Degraded: res.Degraded}
	for _, m := range res.Messages {
		t := m.Type
		if t == "" {
			t = "text"
		}
		out.Messages = append(out.Messages, engine.Message{
			Role: m.Role, Type: t, Content: m.Content,
			ApprovalID: m.ApprovalID, ToolName: m.ToolName,
			Arguments: m.Arguments, Status: m.Status,
		})
		if (t == "approval" || t == "question") && m.Status == "pending" {
			out.Pending = true
			em.ApprovalRequest(m.ApprovalID, m.ToolName, m.Arguments, m.Content)
		}
	}
	return out
}

func sendResultFromEngine(res engine.RunResult) SendMessageResult {
	out := SendMessageResult{Error: res.Error, Degraded: res.Degraded}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, ChatMessageDTO{
			Role: m.Role, Type: m.Type, Content: m.Content, ApprovalID: m.ApprovalID,
			ToolName: m.ToolName, Arguments: m.Arguments, Status: m.Status,
		})
	}
	return out
}
