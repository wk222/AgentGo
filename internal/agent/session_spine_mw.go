package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"

	"agentgo/internal/memory"
	"agentgo/internal/sessions"
	"agentgo/internal/workspace"
)

// SessionSpineMiddleware projects flat messages into PyBot-style six-section runtime view.
type SessionSpineMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	workspaceRoot      string
	kernel             *sessions.SessionKernel
	episodicCompressor *memory.EpisodicCompressor
	getDurableTasks    func(ctx context.Context) []string
	maxActiveTurns     int
}

// SessionSpineConfig configures the spine middleware.
type SessionSpineConfig struct {
	WorkspaceRoot   string
	Kernel             *sessions.SessionKernel
	EpisodicCompressor *memory.EpisodicCompressor
	GetDurableTasks    func(ctx context.Context) []string
	MaxActiveTurns     int
}

func NewSessionSpineMiddleware(cfg SessionSpineConfig) *SessionSpineMiddleware {
	max := cfg.MaxActiveTurns
	if max <= 0 {
		max = 30
	}
	k := cfg.Kernel
	if k == nil && strings.TrimSpace(cfg.WorkspaceRoot) != "" {
		k = sessions.NewSessionKernel(cfg.WorkspaceRoot, 8, 45)
	}
	return &SessionSpineMiddleware{
		workspaceRoot:      cfg.WorkspaceRoot,
		kernel:             k,
		episodicCompressor: cfg.EpisodicCompressor,
		getDurableTasks:    cfg.GetDurableTasks,
		maxActiveTurns:     max,
	}
}

func (m *SessionSpineMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if len(state.Messages) == 0 {
		return ctx, state, nil
	}

	var durable []string
	if m.getDurableTasks != nil {
		durable = m.getDurableTasks(ctx)
	}

	snap := sessions.SpineSnapshotFromContext(ctx)
	snap = m.enrichIsolation(ctx, snap)
	if m.kernel != nil {
		snap = m.kernel.EnrichSnapshot(snap, state.Messages)
		auto := m.kernel.ShouldCompress(state.Messages)
		if m.episodicCompressor != nil {
			scope := SessionIDFromContext(ctx)
			if scope == "" {
				scope = "session"
			}
			if line := m.episodicCompressor.CompressIfNeeded(ctx, scope, state.Messages, auto); line != "" {
				snap.EpisodicSummary = append(snap.EpisodicSummary, line)
			}
		}
	}

	var wsRules string
	if strings.TrimSpace(m.workspaceRoot) != "" {
		wsRules = workspace.NewWorkspaceManager(m.workspaceRoot).BuildContext()
	}

	view := sessions.CompileProjectedView(state.Messages, sessions.CompileOptions{
		MaxActiveTurns: m.maxActiveTurns,
		DurableTasks:   durable,
		WorkspaceRules: wsRules,
		Snapshot:       snap,
	})

	sessionID := SessionIDFromContext(ctx)
	policy := CanvasPolicyFromContext(ctx)
	budgetLimit := policy.ContextTokenBudget
	if budgetLimit == 0 {
		budgetLimit = 32768
	}
	report := InspectContextBudget(ctx, sessionID, view, budgetLimit)
	SaveContextBudgetReport(sessionID, report)

	newMsgs, err := view.Render(ctx)
	if err != nil {
		return ctx, state, err
	}
	state.Messages = newMsgs
	return ctx, state, nil
}

func (m *SessionSpineMiddleware) enrichIsolation(ctx context.Context, snap sessions.SpineSnapshot) sessions.SpineSnapshot {
	sm := SessionModeFromContext(ctx)
	if sm.Profile == "" {
		return snap
	}
	line := sessions.FormatIsolationHint(string(sm.Profile), m.workspaceRoot)
	// avoid duplicate
	for _, existing := range snap.Isolation {
		if existing == line {
			return snap
		}
	}
	snap.Isolation = append(snap.Isolation, line)
	return snap
}

func maxActiveTurnsFor(ctx context.Context) int {
	policy := CanvasPolicyFromContext(ctx)
	if policy.MaxIterations > 15 {
		return 40
	} else if policy.MaxIterations < 10 {
		return 20
	}
	return 30
}
