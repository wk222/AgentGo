package agent

import (
	"context"
	"fmt"

	"agentgo/internal/config"
)

// ModeProfile mirrors PyBot assistant | app_matrix | admin.
type ModeProfile string

const (
	ModeAssistant ModeProfile = "assistant"
	ModeAppMatrix ModeProfile = "app_matrix"
	ModeAdmin     ModeProfile = "admin"
)

// SessionMode holds per-run PyBot profile config.
type SessionMode struct {
	Profile ModeProfile
	Canvas  ExecutionCanvas
}

func DefaultSessionMode() SessionMode {
	return SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced}
}

type modeCtxKey struct{}

func WithSessionMode(ctx context.Context, m SessionMode) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modeCtxKey{}, m.Normalized())
}

func SessionModeFromContext(ctx context.Context) SessionMode {
	if v, ok := sessionModeFromContext(ctx); ok {
		return v
	}
	return DefaultSessionMode()
}

func sessionModeFromContext(ctx context.Context) (SessionMode, bool) {
	if ctx == nil {
		return SessionMode{}, false
	}
	if v, ok := ctx.Value(modeCtxKey{}).(SessionMode); ok {
		return v.Normalized(), true
	}
	return SessionMode{}, false
}

// ModeHints returns system-prompt augmentation for profile.
func (m SessionMode) ModeHints() string {
	p, c := m.normalized()
	return fmt.Sprintf(`[ModeProfile: %s; ExecutionCanvas: %s]
- assistant: answer simple questions directly; for task requests, follow inspect -> plan -> act -> verify and finish the work.
- app_matrix: orchestrate tools/workflows/apps across boundaries.
- admin: persistent operator; plan before destructive actions.
- focused canvas: investigation and read-only reasoning; do not attempt workspace edits or command execution.
- balanced canvas: complete ordinary coding work with approvals, review the diff, and verify the result.
- deep canvas: use broader investigation and delegation only when complexity justifies it; still prefer the smallest decisive check.
- For dashboards, CPU/memory/health metrics, or interactive cards: call render_ui (component metric/card/markdown) with JSON data; do not claim tools are unavailable.`, p, c)
}

// EnvSessionMode reads AGENTGO_MODE_PROFILE.
func EnvSessionMode() SessionMode {
	m := DefaultSessionMode()
	if v := config.ModeProfile.Get(); v != "" {
		m.Profile = ModeProfile(v)
	}
	if v := config.ExecutionCanvas.Get(); v != "" {
		m.Canvas = ExecutionCanvas(v)
	}
	return m.Normalized()
}

func ParseSessionMode(profile, canvas string) SessionMode {
	m := DefaultSessionMode()
	if profile != "" {
		m.Profile = ModeProfile(profile)
	}
	if canvas != "" {
		m.Canvas = ExecutionCanvas(canvas)
	}
	return m.Normalized()
}

func (m SessionMode) Normalized() SessionMode {
	p, c := m.normalized()
	return SessionMode{Profile: p, Canvas: c}
}

func (m SessionMode) MaxIterations() int {
	_, c := m.normalized()
	return CanvasPolicyFor(c).MaxIterations
}

func (m SessionMode) DistillIntervalHours() int {
	_, c := m.normalized()
	return CanvasPolicyFor(c).DistillIntervalHours
}

func (m SessionMode) SummarizationTokenTrigger() int {
	_, c := m.normalized()
	return CanvasPolicyFor(c).SummarizationTokens
}

func EinoAgentsMDEnabled() bool   { return !config.DisableAgentsMD.On() }
func EinoSkillMWEnabled() bool    { return config.EinoSkillMW.On() }
func EinoPlanTaskMWEnabled() bool { return config.EinoPlanTaskMW.On() }

func TurnLoopEnabled() bool { return config.TurnLoop.On() }
