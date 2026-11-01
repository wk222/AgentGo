package bridge

import (
	"agentgo/internal/core"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/interactive"
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
	m := s.rt.registerPending(sessionID, userText, p)
	s.emitPendingMessages(sessionID, []ChatMessageDTO{m})
	return m
}

// Desktop notifications adapt stored results; registering a pause belongs to Runtime.
func (s *AppService) emitPendingMessages(sessionID string, messages []ChatMessageDTO) {
	if !s.eventsOn() {
		return
	}
	for _, m := range messages {
		if m.Status != "pending" {
			continue
		}
		if m.Type == "question" {
			var q interactive.QuestionPayload
			if err := json.Unmarshal([]byte(m.Arguments), &q); err != nil || q.Prompt == "" {
				q.Prompt = m.Content
			}
			s.emitQuestion(&q, m.ApprovalID, sessionID)
		} else if m.Type == "approval" {
			s.emit("approval:pending", map[string]any{
				"session_id": sessionID, "approval_id": m.ApprovalID,
				"tool_name": m.ToolName, "arguments": m.Arguments, "prompt": m.Content,
			})
		}
	}
}

func (rt *Runtime) registerPending(sessionID, userText string, p *agent.PendingApproval) ChatMessageDTO {
	interruptID := p.InterruptID
	if interruptID == "" {
		interruptID = p.ApprovalID
	}
	approvalID := p.ApprovalID
	if approvalID == "" {
		approvalID = interruptID
	}
	rt.pending.Set(core.PendingRun{
		SessionID: sessionID, UserText: userText,
		ToolName: p.ToolName, Arguments: p.Arguments,
		ApprovalID: approvalID, InterruptID: interruptID,
	})
	if p.ToolName == "ask_user" && p.Question != nil {
		qArgs := p.Arguments
		if strings.TrimSpace(qArgs) == "" {
			if raw, err := json.Marshal(p.Question); err == nil {
				qArgs = string(raw)
			}
		}
		return ChatMessageDTO{
			Role: "assistant", Type: "question", ApprovalID: p.ApprovalID,
			Content: p.Question.Prompt, ToolName: "ask_user", Arguments: qArgs, Status: "pending",
		}
	}
	// Say why the tool needs a decision when the policy gave a reason (for
	// example "this reaches outside the workspace; approving opens that folder"),
	// so the card is more than a tool name.
	prompt := "等待审批: " + p.ToolName
	if why := rt.approvalReason(p.ApprovalID); why != "" {
		prompt = why
	}
	return ChatMessageDTO{
		Role: "assistant", Type: "approval", ApprovalID: p.ApprovalID,
		ToolName: p.ToolName, Arguments: p.Arguments, Status: "pending",
		Content: prompt,
	}
}

// approvalReason is the policy's explanation stored with a pending approval
// (the text after the generic "Review required." line), or "".
func (s *AppService) approvalReason(approvalID string) string {
	return s.rt.approvalReason(approvalID)
}

func (rt *Runtime) approvalReason(approvalID string) string {
	if approvalID == "" || rt == nil || rt.Approvals() == nil {
		return ""
	}
	req, err := rt.Approvals().GetRequest(context.Background(), approvalID)
	if err != nil || req == nil {
		return ""
	}
	const marker = "Review required."
	i := strings.Index(req.Prompt, marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(req.Prompt[i+len(marker):])
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
	if pr.ToolName == agent.ArmorBreakToolName {
		// "Start the jailbreak?" is not a paused tool call: there is no checkpoint,
		// approving it is what starts the rollback and the continuation.
		return runner.ContinueArmorBreak(ctx, rt.AgentLLMSettings(), pr.SessionID, pr.Arguments, nil)
	}
	if pr.InterruptID == "" {
		return nil, errors.New("该审批没有对应的检查点，无法恢复")
	}
	if pr.ResumeKind == "matrix" {
		return runner.ResumeMatrixSupervisor(ctx, rt.AgentLLMSettings(), pr.SessionID, pr.InterruptID, resume)
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
	if approved {
		// A conversation that is about to carry on is at work before it leaves the
		// pending store, and until the resumed run registers its cancel handle: the
		// two overlap, so a reader of GetRunningWork never finds it in neither place.
		if p, ok := s.rt.pending.Get(approvalID); ok && p.ResumeKind != "workflow" && p.ResumeKind != "matrix" {
			defer s.rt.resuming.begin(p.SessionID)()
		}
	}
	flow := core.ApprovalFlow{Approvals: s.rt.Approvals(), Pending: s.rt.pending, Relay: &s.rt.relay}
	dec, out, complete, ok := flow.Decide(ctx, approvalID, approved, note, resolvedBy, finalArgs)
	if !ok {
		return out
	}
	defer complete()
	resume, pr := dec.Resume, dec.Run
	if s.eventsOn() {
		s.emit("approval:resolved", map[string]any{
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
			if s.eventsOn() {
				if next, _ := resp["approval_id"].(string); next != "" {
					s.emit("approval:pending", map[string]any{
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
		if s.eventsOn() {
			s.emit("workflow:resumed", map[string]any{
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
			if tap := eventTapFrom(ctx); tap != nil {
				tap.Emit(ev)
			}
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
	rctx = s.transcript().RecordEvidence(rctx, pr.SessionID)
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
	if runRes.PendingApproval == nil && pr.ResumeKind != "matrix" {
		s.rt.fireStopHook(pr.SessionID, runRes.Content) // the resumed turn has ended
	}
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
	if s.eventsOn() && pr.ResumeKind != "matrix" {
		s.emit("chat:done", map[string]any{
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
		if s.eventsOn() {
			s.emit("matrix:resumed", map[string]any{
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
