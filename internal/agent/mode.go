package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	p, _ := m.normalized()
	return fmt.Sprintf(`[ModeProfile: %s]
- assistant: conversational helper, prefer concise answers.
- app_matrix: orchestrate tools/workflows/apps across boundaries.
- admin: persistent operator; plan before destructive actions.
- For dashboards, CPU/memory/health metrics, or interactive cards: call render_ui (component metric/card/markdown) with JSON data; do not claim tools are unavailable.`, p)
}

// EnvSessionMode reads AGENTGO_MODE_PROFILE.
func EnvSessionMode() SessionMode {
	m := DefaultSessionMode()
	if v := os.Getenv("AGENTGO_MODE_PROFILE"); v != "" {
		m.Profile = ModeProfile(v)
	}
	if v := os.Getenv("AGENTGO_EXECUTION_CANVAS"); v != "" {
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



func boolEnv(key string) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "1" || strings.EqualFold(raw, "true") {
		return true
	}
	v, _ := strconv.ParseBool(raw)
	return v
}

func EinoAgentsMDEnabled() bool   { return !boolEnv("AGENTGO_DISABLE_AGENTSMD") }
func EinoSkillMWEnabled() bool    { return boolEnv("AGENTGO_EINO_SKILL_MW") }
func EinoPlanTaskMWEnabled() bool { return boolEnv("AGENTGO_EINO_PLANTASK") }

func TurnLoopEnabled() bool { return boolEnv("AGENTGO_TURN_LOOP") }
