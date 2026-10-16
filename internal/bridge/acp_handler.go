package bridge

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"agentgo/internal/acp"
	"agentgo/internal/agent"
)

// ServeACP starts the ACP JSON-RPC stdio server using the given runtime.
func ServeACP(ctx context.Context, rt *Runtime, r io.Reader, w io.Writer) error {
	handler := NewACPBridgeHandler(rt)
	srv := acp.NewServer(handler, r, w)
	return srv.Serve(ctx)
}

// ACPBridgeHandler connects AgentGo Runtime & Eino Engine to the ACP Server.
type ACPBridgeHandler struct {
	rt         *Runtime
	appService *AppService
}

// NewACPBridgeHandler creates a new ACP Bridge Handler.
func NewACPBridgeHandler(rt *Runtime) *ACPBridgeHandler {
	return &ACPBridgeHandler{
		rt:         rt,
		appService: NewAppService(rt),
	}
}

// HandlePrompt processes a user turn from the ACP client, streaming text and thoughts.
func (h *ACPBridgeHandler) HandlePrompt(ctx context.Context, sessionID, prompt string, emit func(update acp.SessionUpdate)) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is empty")
	}

	inThinking := false
	var buf strings.Builder

	streamEmit := func(delta string) {
		buf.WriteString(delta)

		// Parse <think>...</think> stream tags into ACP thoughts
		if strings.Contains(delta, "<think>") {
			inThinking = true
			parts := strings.Split(delta, "<think>")
			if len(parts) > 0 && parts[0] != "" {
				emit(acp.SessionUpdate{
					SessionUpdate: "agent_message_chunk",
					Content:       &acp.UpdateContent{Type: "text", Text: parts[0]},
				})
			}
			if len(parts) > 1 && parts[1] != "" {
				emit(acp.SessionUpdate{
					SessionUpdate: "agent_thought_chunk",
					Content:       &acp.UpdateContent{Type: "text", Text: parts[1]},
				})
			}
			return
		}

		if strings.Contains(delta, "</think>") {
			inThinking = false
			parts := strings.Split(delta, "</think>")
			if len(parts) > 0 && parts[0] != "" {
				emit(acp.SessionUpdate{
					SessionUpdate: "agent_thought_chunk",
					Content:       &acp.UpdateContent{Type: "text", Text: parts[0]},
				})
			}
			if len(parts) > 1 && parts[1] != "" {
				emit(acp.SessionUpdate{
					SessionUpdate: "agent_message_chunk",
					Content:       &acp.UpdateContent{Type: "text", Text: parts[1]},
				})
			}
			return
		}

		if inThinking {
			emit(acp.SessionUpdate{
				SessionUpdate: "agent_thought_chunk",
				Content:       &acp.UpdateContent{Type: "text", Text: delta},
			})
			return
		}

		emit(acp.SessionUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       &acp.UpdateContent{Type: "text", Text: delta},
		})
	}

	res := h.appService.sendMessageCore(ctx, sessionID, prompt, nil, streamEmit)
	if res.Error != "" {
		return "", fmt.Errorf("%s", res.Error)
	}

	var finalContent string
	for _, m := range res.Messages {
		if m.Role == "assistant" && m.Content != "" {
			finalContent = m.Content
			break
		}
	}
	if finalContent == "" {
		finalContent = buf.String()
	}

	return finalContent, nil
}

// CancelSession stops active generation in the given session.
func (h *ACPBridgeHandler) CancelSession(sessionID string) bool {
	runner := h.rt.AgentRunner()
	if runner == nil {
		return false
	}
	return runner.CancelSessionRun(sessionID)
}

// NewSession creates or prepares an ACP session.
func (h *ACPBridgeHandler) NewSession(params acp.NewSessionParams) (*acp.NewSessionResult, error) {
	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("agentgo-acp-%d", time.Now().UnixMilli())
	}

	target := params.WorkspaceTarget
	if target == "" {
		target = params.CWD
	}
	if target != "" {
		_, _ = h.rt.SetWorkspaceRoot(target)
	}

	cfg := h.rt.LLMConfig()
	modelName := cfg.Model
	if modelName == "" {
		modelName = "default"
	}

	return &acp.NewSessionResult{
		SessionID: sessionID,
		Modes: &acp.SessionModes{
			CurrentModeID:  agent.DefaultHarnessID,
			AvailableModes: acpModeItems(),
		},
		ConfigOptions: []acp.ConfigOptionItem{
			{
				ID:           "model",
				Name:         "Model Selection",
				Type:         "select",
				Category:     "model",
				CurrentValue: modelName,
				Options: []acp.ConfigOptionEntry{
					{Value: modelName, Name: fmt.Sprintf("AgentGo Active (%s)", modelName)},
					{Value: "deepseek-chat", Name: "DeepSeek V3"},
					{Value: "deepseek-reasoner", Name: "DeepSeek R1 (深度思考)"},
					{Value: "claude-sonnet-4-5", Name: "Claude 3.7 Sonnet"},
				},
			},
		},
	}, nil
}

// SetSessionMode maps ACP mode to AgentGo session mode profile.
func (h *ACPBridgeHandler) SetSessionMode(sessionID, modeID string) error {
	runner := h.rt.AgentRunner()
	if runner == nil {
		return nil
	}

	// Editors choose among the same harness presets as the desktop UI. Ids from older
	// editor configs (architect/coder/ask) still resolve; anything else keeps the default.
	sm := agent.DefaultSessionMode()
	if h, ok := agent.ResolveHarness(modeID); ok {
		sm = h.Mode()
	}

	runner.SetSessionModeForSession(sessionID, sm)
	return nil
}

func acpModeItems() []acp.ModeItem {
	hs := agent.Harnesses()
	items := make([]acp.ModeItem, 0, len(hs))
	for _, h := range hs {
		items = append(items, acp.ModeItem{ID: h.ID, Name: h.Label, Description: h.Desc})
	}
	return items
}

// CloseSession releases session resources.
func (h *ACPBridgeHandler) CloseSession(sessionID string) error {
	runner := h.rt.AgentRunner()
	if runner != nil {
		runner.ClearSessionModeForSession(sessionID)
	}
	return nil
}

// GenerateInlineEdit delegates inline code modification to AppService.
func (h *ACPBridgeHandler) GenerateInlineEdit(filePath, language, selectedText, contextBefore, contextAfter, instruction string) (map[string]any, error) {
	res := h.appService.GenerateInlineEdit(filePath, language, selectedText, contextBefore, contextAfter, instruction)
	return res, nil
}

// GetSystemStatus returns current model, active mode, workspace root and health state.
func (h *ACPBridgeHandler) GetSystemStatus() (map[string]any, error) {
	return h.appService.GetSystemStatus(), nil
}

// TerminalQuickFix analyzes failed terminal execution and suggests corrections.
func (h *ACPBridgeHandler) TerminalQuickFix(command, output string, exitCode int, cwd string) (map[string]any, error) {
	return h.appService.TerminalQuickFix(command, output, exitCode, cwd), nil
}

// GenerateCompletion generates inline code completion (Ghost Text) for CodeFuse / OpenSumi.
func (h *ACPBridgeHandler) GenerateCompletion(prefix, suffix, language string) (map[string]any, error) {
	return h.appService.GenerateCompletion(prefix, suffix, language), nil
}

// FixProblem analyzes a compiler / LSP diagnostic error and proposes an immediate fix.
func (h *ACPBridgeHandler) FixProblem(filePath, code, errorMessage string) (map[string]any, error) {
	return h.appService.FixProblem(filePath, code, errorMessage), nil
}
