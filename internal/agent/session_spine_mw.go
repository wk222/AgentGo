package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	}
}

func (m *SessionSpineMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if len(state.Messages) == 0 {
		return ctx, state, nil
	}

	sessionID := SessionIDFromContext(ctx)
	if m.store != nil && sessionID != "" && sessionID != "desktop" {
		dbMsgs, err := m.store.GetMessages(ctx, sessionID, 500)
		if err == nil && len(dbMsgs) > 0 {
			var stateUserMsgs []*schema.Message
			for _, msg := range state.Messages {
				if msg.Role == schema.User {
					content := strings.TrimSpace(msg.Content)
					isSystemInject := false
					for _, prefix := range []string{
						"[System Summary]", "[Injected Memory Context]", "[Environment Feedback]",
						"[Workflow Context]", "[App Matrix]", "[Context Hygiene]",
						"[Team Memory]", "[Isolation]", "[Mode Profile]",
					} {
						if strings.HasPrefix(content, prefix) {
							isSystemInject = true
							break
						}
					}
					if !isSystemInject {
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

			for i := 0; i < len(stateUserMsgs); i++ {
				stateIdx := len(stateUserMsgs) - 1 - i
				dbIdx := len(dbUserMsgs) - 1 - i
				if dbIdx < 0 {
					break
				}
				stateMsg := stateUserMsgs[stateIdx]
				dbMsg := dbUserMsgs[dbIdx]

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
		snap = m.kernel.EnrichSnapshot(snap, state.Messages)
		auto := m.kernel.ShouldCompress(state.Messages)
		if m.episodicCompressor != nil {
			scope := sessionID
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
