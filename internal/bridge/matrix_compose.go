package bridge

import (
	"context"
	"fmt"
	"strings"

	"agentgo/internal/capability"
	"agentgo/internal/core"
)

// The App Matrix graph lives in core (core/matrix_compose.go); this binds it to
// this process's model config, workflow executor, inner apps and agent runner.

// RunMatrixCompose orchestrates one capability event.
func (rt *Runtime) RunMatrixCompose(ctx context.Context, ev capability.Event) (*core.MatrixState, error) {
	if rt == nil {
		return nil, fmt.Errorf("runtime nil")
	}
	m := &core.MatrixOrchestrator{
		RunWorkflow:   rt.wfExec,
		Runner:        rt.agentRunner,
		LLM:           rt.AgentLLMSettings(),
		TraceContext:  matrixTraceContext,
		RegisterPause: rt.matrixRegisterPause,
		InvokeApp: func(ctx context.Context, name, input string) (string, string) {
			res := rt.InvokeInnerApp(ctx, name, input, "", "", "")
			return res.Output, res.Error
		},
	}
	if cfg := rt.LLMConfig(); strings.TrimSpace(cfg.APIKey) != "" {
		m.PlanLLM = func(ctx context.Context, system, user string) (string, error) {
			return ChatOnce(ctx, cfg, system, user)
		}
	}
	return m.Run(ctx, ev)
}
