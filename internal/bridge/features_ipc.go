package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/interactive"
	"agentgo/internal/taskhub"
	"agentgo/internal/workflow"
)

// --- Background tasks (SSE-style via Wails task:event) ---

func (s *AppService) StartBackgroundTask(kind, sessionID, input string) map[string]any {
	if s.rt.taskHub == nil {
		return map[string]any{"success": false, "error": "task hub unavailable"}
	}
	t, err := s.rt.taskHub.Start(kind, sessionID, input)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "task": t}
}

func (s *AppService) ListBackgroundTasks(limit int) []taskhub.Task {
	if s.rt.taskHub == nil {
		return nil
	}
	list, _ := s.rt.taskHub.List(limit)
	return list
}

// SubscribeTaskEvents replays events after_seq then relies on live task:event emissions.
func (s *AppService) SubscribeTaskEvents(taskID string, afterSeq int64) []taskhub.Event {
	if s.rt.taskHub == nil {
		return nil
	}
	events, _ := s.rt.taskHub.EventsSince(taskID, afterSeq)
	return events
}

// --- Scheduler ---

func (s *AppService) CreateScheduledJob(title, spec, prompt, sessionID string) map[string]any {
	if s.rt.sched == nil {
		return map[string]any{"success": false, "error": "scheduler unavailable"}
	}
	j, err := s.rt.sched.Create(title, spec, prompt, sessionID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "job": j}
}

func (s *AppService) ListScheduledJobs() []map[string]any {
	if s.rt.sched == nil {
		return nil
	}
	jobs, _ := s.rt.sched.List()
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, map[string]any{
			"id": j.ID, "title": j.Title, "spec": j.Spec, "prompt": j.Prompt,
			"next_run_at": j.NextRunAt, "interval_sec": j.IntervalSec, "enabled": j.Enabled,
		})
	}
	return out
}

func (s *AppService) DeleteScheduledJob(id string) map[string]any {
	if s.rt.sched == nil {
		return map[string]any{"success": false}
	}
	return map[string]any{"success": s.rt.sched.Delete(id) == nil}
}

// --- Interactive ask_user ---

func (s *AppService) AnswerQuestion(interruptID, answerJSON string) map[string]any {
	ctx := context.Background()
	runner := s.rt.AgentRunner()
	if runner == nil {
		return map[string]any{"success": false, "error": "runner unavailable"}
	}
	cfg := s.rt.LLMConfig()
	llm := agent.LLMSettings{APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model}
	p, ok := s.rt.pending.Get(interruptID)
	if !ok {
		p, ok = s.rt.pending.Get("interrupt:" + interruptID)
	}
	sessionID := ""
	if ok {
		sessionID = p.SessionID
	}
	res, err := runner.ResumeInterrupt(ctx, llm, sessionID, interruptID, answerJSON)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	s.rt.pending.Delete(interruptID)
	if res.Content != "" && sessionID != "" {
		_ = s.rt.Sessions().AppendMessage(ctx, sessionID, "assistant", res.Content, "text", nil)
	}
	out := map[string]any{"success": true, "content": res.Content}
	if res.PendingApproval != nil {
		out["pending"] = res.PendingApproval
	}
	return out
}

// --- Skills ---

func (s *AppService) ListSkills() []map[string]any {
	if s.rt.skillLoader == nil {
		return s.ListCapabilities("skill")
	}
	list := s.rt.skillLoader.Reload()
	out := make([]map[string]any, 0, len(list))
	for _, sk := range list {
		s.rt.CapabilityBus().Register("skill", sk.Name, sk.Scope, map[string]string{"path": sk.Path})
		out = append(out, map[string]any{
			"id": sk.ID, "name": sk.Name, "description": sk.Description, "path": sk.Path, "scope": sk.Scope,
		})
	}
	return out
}

func (s *AppService) GetSkillContext(skillIDs []string) string {
	if s.rt.skillLoader == nil {
		return ""
	}
	return s.rt.skillLoader.ContextBlock(skillIDs)
}

// --- Workflows (Coze-style list + run) ---

func (s *AppService) ListWorkflows() []workflow.Definition {
	if s.rt.wfStore == nil {
		return nil
	}
	list, _ := s.rt.wfStore.List()
	return list
}

func (s *AppService) SaveWorkflow(def workflow.Definition) map[string]any {
	if s.rt.wfStore == nil {
		return map[string]any{"success": false}
	}
	if err := s.rt.wfStore.Save(def); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	_ = s.rt.SyncWorkflowTools()
	return map[string]any{"success": true, "id": def.ID}
}

func (s *AppService) RunWorkflow(workflowID, input string) map[string]any {
	if s.rt.wfExec == nil {
		return map[string]any{"success": false, "error": "workflow executor unavailable"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := s.rt.wfExec(ctx, workflowID, input)
	if err != nil {
		if ie, ok := workflow.AsInterrupt(err); ok {
			return s.rt.workflowInterruptResponse(workflowID, ie)
		}
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "output": out}
}

// ResumeWorkflow continues a checkpointed PyFlow run (Eino compose.ResumeWithData).
func (s *AppService) ResumeWorkflow(workflowID, checkPointID, interruptID, resumeJSON string) map[string]any {
	if s.rt.wfResume == nil {
		return map[string]any{"success": false, "error": "workflow resume unavailable"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := s.rt.wfResume(ctx, workflowID, checkPointID, interruptID, resumeJSON)
	if err != nil {
		if ie, ok := workflow.AsInterrupt(err); ok {
			return s.rt.workflowInterruptResponse(workflowID, ie)
		}
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "output": out}
}

// CreateAdminDurableTask enqueues a multi-step admin background goal.
func (s *AppService) CreateAdminDurableTask(goal string) map[string]any {
	ctx := context.Background()
	task, err := s.rt.CreateAdminDurableTask(ctx, strings.TrimSpace(goal))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success": true,
		"id":      task.ID, "goal": task.Goal, "steps": task.Steps, "status": task.Status,
	}
}

// SetSessionMode sets PyBot ModeProfile × ExecutionCanvas for the ADK runner.
func (s *AppService) SetSessionMode(profile, canvas string) map[string]any {
	if r := s.rt.AgentRunner(); r != nil {
		r.SetSessionMode(agent.ParseSessionMode(profile, canvas))
	}
	sm := agent.DefaultSessionMode()
	if r := s.rt.AgentRunner(); r != nil {
		sm = r.SessionMode()
	}
	return map[string]any{
		"success":        true,
		"profile":        string(sm.Profile),
		"canvas":         string(sm.Canvas),
		"max_iterations": sm.MaxIterations(),
	}
}

// SetSessionModeForSession sets ModeProfile x ExecutionCanvas for one chat session.
func (s *AppService) SetSessionModeForSession(sessionID, profile, canvas string) map[string]any {
	sm := agent.ParseSessionMode(profile, canvas)
	if r := s.rt.AgentRunner(); r != nil {
		r.SetSessionModeForSession(sessionID, sm)
		sm = r.SessionModeForSession(sessionID)
	}
	return map[string]any{
		"success":        true,
		"session_id":     sessionID,
		"profile":        string(sm.Profile),
		"canvas":         string(sm.Canvas),
		"max_iterations": sm.MaxIterations(),
	}
}

// GetSessionModeForSession returns the effective mode for one chat session.
func (s *AppService) GetSessionModeForSession(sessionID string) map[string]any {
	sm := agent.DefaultSessionMode()
	if r := s.rt.AgentRunner(); r != nil {
		sm = r.SessionModeForSession(sessionID)
	}
	return map[string]any{
		"success":        true,
		"session_id":     sessionID,
		"profile":        string(sm.Profile),
		"canvas":         string(sm.Canvas),
		"max_iterations": sm.MaxIterations(),
	}
}

// SetAgenticMode is deprecated and always returns false.
func (s *AppService) SetAgenticMode(enabled bool) map[string]any {
	return map[string]any{"success": true, "agentic": false}
}

func (s *AppService) GetFeatureFlags() map[string]any {
	agentic := false
	sm := agent.EnvSessionMode()
	if r := s.rt.AgentRunner(); r != nil {
		sm = r.SessionMode()
	}
	turnLoop := false
	reductionTrunc := false
	if r := s.rt.AgentRunner(); r != nil {
		reductionTrunc = agent.ReductionTruncationEnabled()
	}
	return map[string]any{
		"background_tasks":    s.rt.taskHub != nil,
		"scheduler":           s.rt.sched != nil,
		"skills_loader":       s.rt.skillLoader != nil,
		"workflow_store":      s.rt.wfStore != nil,
		"agentic_message":     agentic,
		"http_sse_gateway":    s.rt.gatewaySrv != nil,
		"dynamic_python":      true,
		"mode_profile":        string(sm.Profile),
		"execution_canvas":    string(sm.Canvas),
		"enable_subagent":     sm.EnableSubagent(),
		"distill_hours":       sm.DistillIntervalHours(),
		"agentsmd_mw":         agent.EinoAgentsMDEnabled(),
		"eino_skill_mw":       agent.EinoSkillMWEnabled(),
		"turn_loop":           turnLoop,
		"reduction_trunc":     reductionTrunc,
		"workflow_graph_tool": workflow.GraphToolEnabled(),
	}
}

// PushTurnInput is deprecated because the TurnLoop has been removed.
func (s *AppService) PushTurnInput(sessionID, text string, preempt bool) map[string]any {
	return map[string]any{"success": false, "error": "turn loop is deprecated, please use standard chat endpoints"}
}

// CancelAgentRun stops the in-flight ADK agent for a session (immediate cancel).
func (s *AppService) CancelAgentRun(sessionID string) map[string]any {
	if s.rt.runTrack != nil {
		s.rt.runTrack.Finish(sessionID, "cancelled", "")
	}
	if r := s.rt.AgentRunner(); r != nil {
		return map[string]any{"success": r.CancelSessionRun(sessionID)}
	}
	return map[string]any{"success": false, "error": "runner unavailable"}
}

// StopSession is the desktop stop button's compatibility endpoint. It cancels
// the active ADK run.
func (s *AppService) StopSession(sessionID string) map[string]any {
	cancelled := false
	if s.rt.runTrack != nil {
		s.rt.runTrack.Finish(sessionID, "cancelled", "")
	}
	if r := s.rt.AgentRunner(); r != nil {
		cancelled = r.CancelSessionRun(sessionID)
		return map[string]any{"success": true, "cancelled": cancelled}
	}
	return map[string]any{"success": false, "error": "runner unavailable"}
}

// StopTurnLoop stops the long-lived session loop (deprecated).
func (s *AppService) StopTurnLoop(sessionID string) map[string]any {
	return map[string]any{"success": true}
}

// emitQuestion forwards ask_user interrupt to UI.
func (s *AppService) emitQuestion(q *interactive.QuestionPayload, interruptID, sessionID string) {
	if s.app == nil || q == nil {
		return
	}
	b, _ := json.Marshal(q)
	payload := map[string]any{
		"interrupt_id": interruptID,
		"question":     json.RawMessage(b),
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	s.app.Event.Emit("ask:pending", payload)
}
