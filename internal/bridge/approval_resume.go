package bridge

import (
	"context"
	"errors"
	"log"
	"strings"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/sessions"
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

// errNoPendingRun is reported when an approval was granted but nothing is
// waiting to be resumed — typically because the process restarted (pending runs
// live in memory) or the decision was already handled.
var errNoPendingRun = errors.New("没有可恢复的运行（进程已重启，或该审批已被处理）；工具没有被执行")

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
	s.rt.pending.Set(pendingRun{
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
func (rt *Runtime) resumeAgent(ctx context.Context, pr pendingRun, resume *governance.ResumePayload) (*agent.RunResult, error) {
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
	if err := ctx.Err(); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	resume := &governance.ResumePayload{Approved: approved, Arguments: finalArgs}
	if err := s.rt.Approvals().Resolve(ctx, approvalID, approved, note, resolvedBy, resume); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	// The decision is on record: let any surface waiting on it close its dialog,
	// and give it the outcome once the resume below is over.
	s.rt.relay.markDecided(approvalID, approved, resolvedBy)
	out := map[string]any{"success": true}
	defer func() { s.rt.relay.markCompleted(approvalID, out) }()
	if s.app != nil {
		s.app.Event.Emit("approval:resolved", map[string]any{
			"approval_id": approvalID,
			"approved":    approved,
			"arguments":   finalArgs,
		})
	}

	// Claim the right to resume. Only one caller can ever get the pending run.
	pr, found := s.rt.pending.Take(approvalID)
	if !found {
		if approved {
			out["resume_error"] = errNoPendingRun.Error()
		}
		return out
	}
	if finalArgs != "" {
		pr.Arguments = finalArgs
	}

	if pr.ResumeKind == "workflow" {
		wfOut, err := s.rt.resumeWorkflowAfterApproval(ctx, pr, resume)
		if !approved {
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

	runID := s.awaitingRunID(ctx, pr)
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
		s.appendRunStatus(pr.SessionID, runID, "resumed")
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
func (s *AppService) afterResume(approvalID string, pr pendingRun, res *agent.RunResult) []ChatMessageDTO {
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

// awaitingRunID returns the run the log shows as paused for this session, or ""
// when there is none (matrix/workflow runs, sessions without a log, or paths
// that never wrote one). Only a run that is awaiting approval can be closed.
func (s *AppService) awaitingRunID(ctx context.Context, pr pendingRun) string {
	if pr.ResumeKind != "" || pr.SessionID == "" || s.rt.Sessions() == nil {
		return ""
	}
	st, err := s.rt.Sessions().LastRunState(ctx, pr.SessionID)
	if err != nil || st.Status != sessions.RunAwaitingApproval {
		return ""
	}
	return st.RunID
}

// appendRunStatus adds a status fact to an existing run.
func (s *AppService) appendRunStatus(sessionID, runID, state string) {
	if runID == "" {
		return
	}
	if _, err := s.rt.Sessions().AppendRunEvent(context.Background(), sessions.RunEvent{
		SessionID: sessionID, RunID: runID, Kind: sessions.KindStatus,
		PayloadJSON: `{"state":"` + state + `"}`,
	}); err != nil {
		log.Printf("[runlog] session=%s run=%s status: %v", sessionID, runID, err)
	}
}
