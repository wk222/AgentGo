package bridge

import (
	"context"
	"log"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/sessions"
)

func (rt *Runtime) onCapabilityEvent(ev capability.Event) {
	switch ev.Type {
	case "workflow.done":
		log.Printf("[capability] workflow.done %s session=%s", ev.Source, ev.SessionID)
		rt.maybeMatrixFollowup(ev)
	case "tool.compiled":
		log.Printf("[capability] tool.compiled %s", ev.Source)
		rt.maybeMatrixFollowup(ev)
	case "app.registered":
		log.Printf("[capability] app.registered %s", ev.Source)
		rt.maybeMatrixFollowup(ev)
	}
}

func (rt *Runtime) maybeMatrixFollowup(ev capability.Event) {
	if !agent.MatrixAutoFollowupEnabled() {
		return
	}
	runner := rt.agentRunner
	if runner == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		ctx = sessions.AppendWorkflowLine(ctx, sessions.FormatWorkflowContext(
			"能力总线 "+ev.Type+" source="+ev.Source+" session="+ev.SessionID))

		cfg := rt.AgentLLMSettings()
		prompt := matrixEventPromptFromCapability(ev)

		fu := agent.RunMatrixFollowup(ctx, agent.MatrixFollowupInput{
			Event:  ev,
			LLM:    cfg,
			Prompt: prompt,
			RunSupervisor: func(ctx context.Context, p string) (string, bool, error) {
				if !agent.MatrixSupervisorEnabled() || strings.TrimSpace(cfg.APIKey) == "" {
					return "", false, nil
				}
				sctx := matrixTraceContext(ctx, agent.MatrixCoordinatorSession)
				res, err := runner.RunMatrixSupervisor(sctx, cfg, agent.MatrixCoordinatorSession, p)
				if err != nil {
					log.Printf("[app_matrix] supervisor error: %v", err)
					return "", false, err
				}
				if res != nil && res.PendingApproval != nil {
					rt.matrixRegisterPause(res, ev, p)
					log.Printf("[app_matrix] supervisor paused for approval: %s", res.PendingApproval.ApprovalID)
					return strings.TrimSpace(res.Content), true, nil
				}
				if res != nil {
					log.Printf("[app_matrix] supervisor done (%d chars)", len(res.Content))
					return strings.TrimSpace(res.Content), false, nil
				}
				return "", false, nil
			},
			RunCompose: func(ctx context.Context, e capability.Event) (string, string, bool) {
				if !agent.MatrixComposeEnabled() {
					return "", "", false
				}
				st, err := rt.RunMatrixCompose(ctx, e)
				if err != nil {
					log.Printf("[app_matrix] compose error: %v", err)
					return "", err.Error(), false
				}
				if st == nil {
					return "", "", false
				}
				summary := strings.TrimSpace(st.Summary)
				if summary == "" && st.ExecOutput != "" {
					summary = strings.TrimSpace(st.ExecOutput)
				}
				log.Printf("[app_matrix] compose action=%s target=%s", st.PlanAction, st.PlanTarget)
				return summary, st.Error, summary != "" || st.Error != ""
			},
			RunLegacy: func(ctx context.Context, p string) (string, error) {
				s := rt.matrixLegacyFollowup(ctx, runner, ev)
				return s, nil
			},
		})

		if fu.Stopped {
			return
		}
		if fu.Summary != "" {
			rt.publishMatrixOrchestrated(ev, fu.Summary, fu.Error)
		}
	}()
}

func matrixEventPromptFromCapability(ev capability.Event) string {
	payload := map[string]string{}
	switch ev.Type {
	case "workflow.done":
		payload["workflow"] = ev.Source
		payload["output"] = ev.Payload["output"]
	case "tool.compiled":
		payload["tool"] = ev.Source
	case "app.registered":
		payload["app"] = ev.Source
	default:
		payload["detail"] = ev.Type
	}
	for k, v := range ev.Payload {
		if _, ok := payload[k]; !ok {
			payload[k] = v
		}
	}
	return agent.MatrixEventPrompt(ev.Type, ev.Source, ev.SessionID, payload)
}

func (rt *Runtime) matrixLegacyFollowup(ctx context.Context, runner *agent.Runner, ev capability.Event) string {
	prompt := matrixEventPromptFromCapability(ev)
	if prompt == "" {
		return ""
	}
	cfg := rt.AgentLLMSettings()
	mctx := agent.WithSessionMode(
		agent.WithSessionID(ctx, agent.MatrixCoordinatorSession),
		agent.SessionMode{Profile: agent.ModeAppMatrix, Canvas: agent.CanvasDeep},
	)
	res, err := runner.Generate(mctx, cfg, agent.MatrixCoordinatorSession, prompt, nil)
	if err != nil {
		log.Printf("[app_matrix] coordinator error: %v", err)
		return ""
	}
	if res != nil {
		return res.Content
	}
	return ""
}

func (rt *Runtime) publishMatrixOrchestrated(ev capability.Event, summary, orchErr string) {
	log.Printf("[app_matrix] coordinator replied (%d chars)", len(summary))
	if rt.taskHub != nil {
		_, _ = rt.taskHub.Start("matrix_followup", "app_matrix_coordinator", summary)
	}
	if rt.capBus != nil {
		rt.capBus.Publish(capability.Event{
			Type: "matrix.orchestrated", Source: ev.Source, SessionID: ev.SessionID,
			Payload: map[string]string{
				"summary": summary, "event_type": ev.Type, "error": orchErr,
			},
		})
	}
}

func (rt *Runtime) AgentLLMSettings() agent.LLMSettings {
	cfg := rt.LLMConfig()
	return agent.LLMSettings{
		APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model, FallbackModel: cfg.FallbackModel,
	}
}
