package bridge

import (
	"agentgo/internal/core"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/workflow"
)

// This file is the single place where a user's decision on an approval turns
// into action. Transports (the desktop binding, the Crush TUI adapter) only
// translate their request and show the outcome; they do not resume anything.
//
// Guarantees:
//   - A decision is recorded once (the queue rejects a second one), and the right
//     to resume is claimed atomically, so a tool call is resumed at most once.
//   - A resumed run that pauses again (a second approval) is registered exactly
//     like the first pause, so approvals can chain.
//   - Everything that stops a resume from happening is reported as resume_error;
//     "approved" never silently means "nothing ran".
//   - A run that the run log shows as awaiting approval is closed with a new
//     commit point when it is resumed or rejected.

// registerPending records a run that stopped for approval (or a question) and
// returns the chat message that represents it. Used for the first pause of a
// turn and for any later pause of a resumed run.
func (s *AppService) registerPending(sessionID, userText string, p *agent.PendingApproval) ChatMessageDTO {
	interruptID := p.InterruptID
	if interruptID == "" {
		interruptID = p.ApprovalID
	}
	approvalID := p.ApprovalID
	if approvalID == "" {
		approvalID = interruptID
	}
	s.rt.pending.Set(core.PendingRun{
		SessionID: sessionID, UserText: userText,
		ToolName: p.ToolName, Arguments: p.Arguments,
		ApprovalID: approvalID, InterruptID: interruptID,
	})
	if p.ToolName == "ask_user" && p.Question != nil {
		s.emitQuestion(p.Question, p.ApprovalID, sessionID)
		return ChatMessageDTO{
			Role: "assistant", Type: "question", ApprovalID: p.ApprovalID,
			Content: p.Question.Prompt, ToolName: "ask_user", Status: "pending",
		}
	}
	if s.app != nil {
		payload := map[string]any{
			"approval_id": p.ApprovalID,
			"tool_name":   p.ToolName,
			"arguments":   p.Arguments,
			"prompt":      "等待审批: " + p.ToolName,
		}
		if sessionID != "" {
			payload["session_id"] = sessionID
		}
		s.app.Event.Emit("approval:pending", payload)
	}
	return ChatMessageDTO{
		Role: "assistant", Type: "approval", ApprovalID: p.ApprovalID,
		ToolName: p.ToolName, Arguments: p.Arguments, Status: "pending",
		Content: "等待审批: " + p.ToolName,
	}
}

// resumeAgent continues a paused agent run with the user's decision.
func (rt *Runtime) resumeAgent(ctx context.Context, pr core.PendingRun, resume *governance.ResumePayload) (*agent.RunResult, error) {
	if rt.resumeFn != nil {
		return rt.resumeFn(ctx, pr, resume)
	}
	runner := rt.AgentRunner()
	if runner == nil {
		return nil, errors.New("智能体运行器不可用，无法恢复")
	}
	if pr.InterruptID == "" {
		return nil, errors.New("该审批没有对应的检查点，无法恢复")
	}
	if pr.ResumeKind == "matrix" {
		return runner.ResumeMatrixSupervisor(ctx, rt.AgentLLMSettings(), pr.InterruptID, resume)
	}
	return runner.ContinueAfterApproval(ctx, rt.AgentLLMSettings(), pr.SessionID, pr.InterruptID, pr.ToolName, pr.Arguments, true)
}

// resolveApproval records the decision and, when granted, resumes the paused
// run. ctx bounds the resume: cancelling it cancels the resumed run.
//
// The result carries success (the decision was recorded), resume_error (the run
// could not be resumed or failed), messages (what the resumed run produced,
// including a further pending approval) and workflow_output.
func (s *AppService) resolveApproval(ctx context.Context, approvalID string, approved bool, note, resolvedBy, finalArgs string) map[string]any {
	flow := core.ApprovalFlow{Approvals: s.rt.Approvals(), Pending: s.rt.pending, Relay: &s.rt.relay}
	dec, out, complete, ok := flow.Decide(ctx, approvalID, approved, note, resolvedBy, finalArgs)
	if !ok {
		return out
	}
	defer complete()
	resume, pr := dec.Resume, dec.Run
	if s.app != nil {
		s.app.Event.Emit("approval:resolved", map[string]any{
			"approval_id": approvalID,
			"approved":    approved,
			"arguments":   finalArgs,
		})
	}

	if !dec.Found {
		return out
	}

	if pr.ResumeKind == "workflow" {
		wfOut, err := s.rt.resumeWorkflowAfterApproval(ctx, pr, resume)
		if !approved {
			return out
		}
		if ie, paused := workflow.AsInterrupt(err); paused {
			// The resumed run stopped for another approval: register it like the
			// first pause so approvals chain, instead of reporting a failure.
			resp := s.rt.workflowInterruptResponse(pr.WorkflowID, ie)
			out["interrupted"] = true
			for _, k := range []string{"interrupt_kind", "approval_id", "tool_name", "arguments", "checkpoint_id"} {
				if v, ok := resp[k]; ok {
					out[k] = v
				}
			}
			if s.app != nil {
				if next, _ := resp["approval_id"].(string); next != "" {
					s.app.Event.Emit("approval:pending", map[string]any{
						"approval_id": next, "tool_name": resp["tool_name"],
						"arguments": resp["arguments"], "prompt": "等待审批: " + fmt.Sprint(resp["tool_name"]),
					})
				}
			}
			return out
		}
		if err != nil {
			out["resume_error"] = err.Error()
			return out
		}
		out["workflow_output"] = wfOut
		if s.app != nil {
			s.app.Event.Emit("workflow:resumed", map[string]any{
				"approval_id": approvalID,
				"workflow_id": pr.WorkflowID,
				"output":      wfOut,
			})
		}
		return out
	}

	runID := core.AwaitingRunID(ctx, s.rt.Sessions(), pr)
	var finish func(engine.RunResult)
	var live engine.Emitter
	var pausedEvents []engine.Event
	desktop := s.wailsSink()
	if runID != "" && pr.ResumeKind != "matrix" {
		forward := engine.SinkFunc(func(ev engine.Event) {
			if ev.Type == engine.EventApprovalRequest {
				pausedEvents = append(pausedEvents, ev)
				return
			}
			desktop.Emit(ev)
		})
		live, finish = engine.ResumeEmitter(s.logRunEvents(pr.SessionID, forward), runID, pr.SessionID, EinoEngineID)
	}

	if !approved {
		// The run is not continued after a refusal; close it in the log so it
		// does not stay "awaiting approval" forever.
		s.commitRunWith(pr.SessionID, runID, engine.RunResult{}, map[string]any{"resolution": "rejected"})
		if finish != nil {
			finish(engine.RunResult{})
		}
		return out
	}

	if live == nil {
		core.AppendRunStatus(s.rt.Sessions(), pr.SessionID, runID, "resumed")
	}
	rctx, cancel := context.WithTimeout(ctx, engineRunTimeout)
	defer cancel()
	if live != nil {
		live.Status("resumed", nil)
		rctx = bindEngineObservers(rctx, live)
	}
	runRes, err := s.rt.resumeAgent(rctx, pr, resume)
	agent.DebugLogf("resume: kind=%q interruptID=%q err=%v", pr.ResumeKind, pr.InterruptID, err)
	if err == nil && runRes == nil {
		err = errors.New("恢复没有返回结果")
	}
	if err != nil {
		out["resume_error"] = err.Error()
		s.commitRun(pr.SessionID, runID, engine.RunResult{Error: err.Error()})
		if finish != nil {
			finish(engine.RunResult{Error: err.Error()})
		}
		return out
	}

	msgs := s.afterResume(approvalID, pr, runRes)
	out["messages"] = msgs
	final := engine.RunResult{
		Pending: runRes.PendingApproval != nil, Messages: toEngineMessages(msgs),
	}
	if live != nil {
		final = sendResultToEngine(SendMessageResult{Messages: msgs}, live)
	}
	s.commitRun(pr.SessionID, runID, final)
	if finish != nil {
		for _, ev := range pausedEvents {
			desktop.Emit(ev)
		}
		finish(final)
	}
	if s.app != nil && pr.ResumeKind != "matrix" {
		s.app.Event.Emit("chat:done", map[string]any{
			"session_id": pr.SessionID,
			"messages":   msgs,
			"resume":     true,
		})
	}
	return out
}

// afterResume stores and announces what a resumed run produced and registers a
// further pause if the run stopped for another approval.
func (s *AppService) afterResume(approvalID string, pr core.PendingRun, res *agent.RunResult) []ChatMessageDTO {
	msgs := []ChatMessageDTO{}
	if res.Content != "" {
		msgs = append(msgs, ChatMessageDTO{Role: "assistant", Type: "text", Content: res.Content})
	}

	if pr.ResumeKind == "matrix" {
		ev := capability.Event{Type: "matrix.resumed", Source: pr.MatrixEvent}
		s.rt.publishMatrixOrchestrated(ev, res.Content, "")
		if s.app != nil {
			s.app.Event.Emit("matrix:resumed", map[string]any{
				"approval_id": approvalID, "content": res.Content,
			})
		}
		if res.PendingApproval != nil {
			typ, src, _ := strings.Cut(pr.MatrixEvent, ":")
			s.rt.matrixRegisterPause(res, capability.Event{Type: typ, Source: src}, pr.UserText)
		}
		return msgs
	}

	if res.PendingApproval != nil {
		msgs = append(msgs, s.registerPending(pr.SessionID, pr.UserText, res.PendingApproval))
	}
	if pr.SessionID != "" {
		for _, m := range msgs {
			s.persistResumedMessage(pr.SessionID, m)
		}
	}
	return msgs
}

func (s *AppService) persistResumedMessage(sessionID string, m ChatMessageDTO) {
	var meta map[string]any
	if m.Type == "approval" || m.Type == "question" {
		meta = map[string]any{
			"approval_id": m.ApprovalID, "tool_name": m.ToolName,
			"arguments": m.Arguments, "status": m.Status,
		}
	}
	if err := s.rt.Sessions().AppendMessage(context.Background(), sessionID, m.Role, m.Content, m.Type, meta); err != nil {
		log.Printf("[approval] persist resumed message session=%s: %v", sessionID, err)
	}
}

func toEngineMessages(msgs []ChatMessageDTO) []engine.Message {
	out := make([]engine.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, engine.Message{
			Role: m.Role, Type: m.Type, Content: m.Content, ApprovalID: m.ApprovalID,
			ToolName: m.ToolName, Arguments: m.Arguments, Status: m.Status,
		})
	}
	return out
}
