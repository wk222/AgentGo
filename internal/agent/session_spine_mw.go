package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

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
	store              *sessions.Store
	dataDir            string
	workspaceManager   *workspace.WorkspaceManager
}

// SessionSpineConfig configures the spine middleware.
type SessionSpineConfig struct {
	WorkspaceRoot      string
	Kernel             *sessions.SessionKernel
	EpisodicCompressor *memory.EpisodicCompressor
	GetDurableTasks    func(ctx context.Context) []string
	MaxActiveTurns     int
	Store              *sessions.Store
	DataDir            string
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
		store:              cfg.Store,
		dataDir:            cfg.DataDir,
		workspaceManager:   workspace.NewWorkspaceManager(cfg.WorkspaceRoot),
	}
}

func (m *SessionSpineMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if len(state.Messages) == 0 {
		return ctx, state, nil
	}

	sessionID := SessionIDFromContext(ctx)
	if m.store != nil && sessionID != "" && sessionID != "desktop" && needsAttachmentMetadata(state.Messages, sessionID) {
		dbMsgs, err := m.store.GetMessages(ctx, sessionID, 500)
		if err == nil && len(dbMsgs) > 0 {
			var stateUserMsgs []*schema.Message
			for _, msg := range state.Messages {
				if msg != nil && msg.Role == schema.User {
					if !sessions.IsContextMessage(msg) {
						stateUserMsgs = append(stateUserMsgs, msg)
					}
				}
			}

			var dbUserMsgs []sessions.Message
			for _, dbMsg := range dbMsgs {
				if dbMsg.Role == "user" {
					dbUserMsgs = append(dbUserMsgs, dbMsg)
				}
			}

			dbCursor := len(dbUserMsgs) - 1
			for i := 0; i < len(stateUserMsgs); i++ {
				stateIdx := len(stateUserMsgs) - 1 - i
				stateMsg := stateUserMsgs[stateIdx]
				dbIdx := dbCursor
				for dbIdx >= 0 && strings.TrimSpace(dbUserMsgs[dbIdx].Content) != strings.TrimSpace(stateMsg.Content) {
					dbIdx--
				}
				if dbIdx < 0 {
					continue
				}
				dbCursor = dbIdx - 1
				dbMsg := dbUserMsgs[dbIdx]
				if stateMsg.Extra == nil {
					stateMsg.Extra = map[string]any{}
				}
				stateMsg.Extra["agentgo.attachments_checked"] = sessionID
				if len(stateMsg.UserInputMultiContent) > 0 {
					continue
				}

				type MsgMeta struct {
					Images []string `json:"images"`
				}
				var meta MsgMeta
				if dbMsg.MetaJSON != "" {
					_ = json.Unmarshal([]byte(dbMsg.MetaJSON), &meta)
				}

				if len(meta.Images) > 0 {
					var parts []schema.MessageInputPart
					parts = append(parts, schema.MessageInputPart{
						Type: schema.ChatMessagePartTypeText,
						Text: stateMsg.Content,
					})

					for _, imgPath := range meta.Images {
						filename := filepath.Base(imgPath)
						fullPath := filepath.Join(m.dataDir, "attachments", filename)
						imgBytes, readErr := os.ReadFile(fullPath)
						if readErr != nil {
							continue
						}

						ext := strings.ToLower(filepath.Ext(filename))
						mime := "image/jpeg"
						switch ext {
						case ".png":
							mime = "image/png"
						case ".gif":
							mime = "image/gif"
						case ".webp":
							mime = "image/webp"
						}

						b64Data := base64.StdEncoding.EncodeToString(imgBytes)
						parts = append(parts, schema.MessageInputPart{
							Type: schema.ChatMessagePartTypeImageURL,
							Image: &schema.MessageInputImage{
								MessagePartCommon: schema.MessagePartCommon{
									Base64Data: &b64Data,
									MIMEType:   mime,
								},
							},
						})
					}

					if len(parts) > 1 {
						stateMsg.UserInputMultiContent = parts
					}
				}
			}
		}
	}

	var durable []string
	if m.getDurableTasks != nil {
		durable = m.getDurableTasks(ctx)
	}

	snap := sessions.SpineSnapshotFromContext(ctx)
	snap = m.enrichIsolation(ctx, snap)
	if m.kernel != nil {
		snap = m.kernel.EnrichSnapshotWithoutTeam(snap, state.Messages)
		auto := m.kernel.ShouldCompress(state.Messages)
		if m.episodicCompressor != nil {
			scope := sessionID
			if scope == "" {
				scope = "session"
			}
			// Background extraction feeds atomic recall, never a second handoff summary.
			_ = m.episodicCompressor.CachedSummary(ctx, scope, state.Messages, auto)
		}
	}

	var plans []sessions.PlanTaskSnapshot
	if EinoPlanTaskEnabled() && CanvasPolicyFromContext(ctx).PlanTasks && (m.workspaceRoot != "" || m.dataDir != "") {
		var err error
		plans, err = loadPlanTaskSnapshot(ctx, planTaskDirectory(ctx, m.workspaceRoot, m.dataDir))
		if err != nil {
			if ctx.Err() != nil {
				return ctx, state, ctx.Err()
			}
			// A damaged optional plan must not block a conversation that could repair it.
			plans = nil
			snap.ContextHygiene = append(snap.ContextHygiene, "Current PlanTask snapshot unavailable; inspect TaskList/TaskGet before claiming progress: "+err.Error())
		}
	}
	var wsRules string
	if strings.TrimSpace(m.workspaceRoot) != "" {
		if cached, ok := workspace.ContextFromContext(ctx); ok {
			wsRules = cached
		} else {
			wsRules = m.workspaceManager.BuildContext()
		}
	}

	compileInput, reminders := splitSystemReminders(state.Messages)
	recallBudget := memory.PromptTokenBudget(ctx)
	view := sessions.CompileProjectedView(compileInput, sessions.CompileOptions{
		RecallTokenBudget:  &recallBudget,
		KeepActiveHistory:  true,
		MaxActiveTurns:     m.maxActiveTurns,
		PreserveLatestUser: true,
		PlanTasks:          plans,
		DurableTasks:       durable,
		WorkspaceRules:     wsRules,
		Snapshot:           snap,
	})

	policy := CanvasPolicyFromContext(ctx)
	budgetLimit := policy.ContextTokenBudget
	if budgetLimit == 0 {
		budgetLimit = 32768
	}
	limits := modelBudgetFrom(ctx)
	reminderTokens := 0
	for _, reminder := range reminders {
		reminderTokens += messageContextTokens(reminder.msg)
	}
	inspect := func(v *sessions.ProjectedRuntimeView) ContextBudgetReport {
		report := InspectContextBudget(ctx, sessionID, v, budgetLimit, state.ToolInfos)
		report.ModelContextWindow, report.ReservedOutputTokens = limits.Window, limits.Output
		report.TotalTokens += reminderTokens
		if reminderTokens > 0 {
			report.Segments = append(report.Segments, ContextSegment{Source: "protected_reminders", TokenCount: reminderTokens})
		}
		for i := range report.Segments {
			if report.TotalTokens > 0 {
				report.Segments[i].Percentage = float64(report.Segments[i].TokenCount) * 100 / float64(report.TotalTokens)
			}
		}
		report.Finalize()
		return report
	}
	report := inspect(view)
	// Progressively tighten only historical messages and tool payloads. Never
	// truncate the current user request or silently remove system/workspace rules.
	beforeTokens := report.TotalTokens
	tightTurns := m.maxActiveTurns
	for pass := 0; pass < 4 && report.TotalTokens > budgetLimit*85/100; pass++ {
		tightTurns = max(4, tightTurns*2/3)
		hardLimit := 0
		if pass == 1 {
			hardLimit = 12000
		} else if pass >= 2 {
			hardLimit = 4000
		}
		view = sessions.CompileProjectedView(compileInput, sessions.CompileOptions{
			RecallTokenBudget: &recallBudget,
			MaxActiveTurns:    tightTurns, PreserveLatestUser: true, RecentTailTurns: 2, MaxStaleToolChars: 600, HardToolResultChars: hardLimit,
			PlanTasks: plans, DurableTasks: durable, WorkspaceRules: wsRules, Snapshot: snap,
		})
		report = inspect(view)
	}
	if saved := beforeTokens - report.TotalTokens; saved > 0 {
		report.TruncationLog = append(report.TruncationLog, TruncationDecision{Source: "preflight_context_gate", Reason: "Reduced historical window and tool-output excerpts; current user request and rules preserved", Removed: saved})
	}
	if report.TotalTokens > budgetLimit {
		report.TruncationLog = append(report.TruncationLog, TruncationDecision{Source: "protected_context", Reason: "Protected instructions/current request or tool schemas still exceed estimated budget; they were not silently discarded", Removed: 0})
	}
	SaveContextBudgetReport(sessionID, report)
	if limits.Input > 0 && report.TotalTokens > limits.Input {
		return ctx, state, fmt.Errorf("上下文预计 %d tokens，超过当前模型可用输入预算 %d（窗口 %d，已预留输出 %d）；当前要求和规则已保留，请减少附件、工具或新建会话后重试", report.TotalTokens, limits.Input, limits.Window, limits.Output)
	}

	if report.TotalTokens > budgetLimit {
		return ctx, state, fmt.Errorf("estimated context %d tokens exceeds configured input budget %d; protected instructions and the current request were preserved", report.TotalTokens, budgetLimit)
	}
	newMsgs, err := view.Render(ctx)
	if err != nil {
		return ctx, state, err
	}
	newMsgs = restoreSystemReminders(newMsgs, reminders)
	if err := recordProjectionRewrite(ctx, state.Messages, newMsgs); err != nil {
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
	// Thresholds sit between the presets (6 / 20 / 30), so a preset's iteration
	// budget can change without silently changing how much history it keeps.
	if policy.MaxIterations > 25 {
		return 40
	} else if policy.MaxIterations < 10 {
		return 20
	}
	return 30
}

func needsAttachmentMetadata(msgs []*schema.Message, sessionID string) bool {
	for _, m := range msgs {
		if m == nil || m.Role != schema.User || sessions.IsContextMessage(m) {
			continue
		}
		if len(m.UserInputMultiContent) == 0 && (m.Extra == nil || m.Extra["agentgo.attachments_checked"] != sessionID) {
			return true
		}
	}
	return false
}
