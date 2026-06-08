package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/taskhub"
)

// AppService exposes AgentGo backend to the Wails frontend via IPC.
type AppService struct {
	rt  *Runtime
	app *application.App
}

func NewAppService(rt *Runtime) *AppService {
	return &AppService{rt: rt}
}

func (s *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.app = application.Get()
	if s.rt.taskHub != nil {
		s.rt.taskHub.AddEmitter(func(taskID string, ev taskhub.Event) {
			if s.app != nil {
				s.app.Event.Emit("task:event", map[string]any{
					"task_id": taskID, "seq": ev.Seq, "type": ev.Type, "payload": ev.Payload,
				})
			}
		})
	}
	return nil
}

// --- DTOs for JS ---

type ChatMessageDTO struct {
	Role    string `json:"role"`
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	// approval fields
	ApprovalID string `json:"approval_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Status     string `json:"status,omitempty"`
	// A2UI fields
	Component  string `json:"component,omitempty"`
	DataJSON   string `json:"data_json,omitempty"`
	InteractID string `json:"interact_id,omitempty"`
}

type SendMessageResult struct {
	Messages []ChatMessageDTO `json:"messages"`
	Error    string           `json:"error,omitempty"`
}

type ApprovalDTO struct {
	ID        string `json:"approval_id"`
	Summary   string `json:"summary"`
	Prompt    string `json:"prompt"`
	ToolName  string `json:"tool_name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Status    string `json:"status"`
}

// GetLLMConfig returns current LLM settings (api_key masked).
func (s *AppService) GetLLMConfig() map[string]any {
	cfg := s.rt.LLMConfig()
	masked := ""
	if cfg.APIKey != "" {
		if len(cfg.APIKey) > 8 {
			masked = cfg.APIKey[:4] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
		} else {
			masked = "***"
		}
	}
	return map[string]any{
		"api_base":       cfg.APIBase,
		"api_key_set":    cfg.APIKey != "",
		"api_key_hint":   masked,
		"model":          cfg.Model,
		"fallback_model": cfg.FallbackModel,
	}
}

// SetLLMConfig persists LLM settings from the settings UI.
func (s *AppService) SetLLMConfig(apiBase, apiKey, model, fallbackModel string) map[string]any {
	cfg := s.rt.LLMConfig()
	if strings.TrimSpace(apiBase) != "" {
		cfg.APIBase = strings.TrimSpace(apiBase)
	}
	if strings.TrimSpace(apiKey) != "" {
		cfg.APIKey = strings.TrimSpace(apiKey)
	}
	if strings.TrimSpace(model) != "" {
		cfg.Model = strings.TrimSpace(model)
	}
	if strings.TrimSpace(fallbackModel) != "" {
		cfg.FallbackModel = strings.TrimSpace(fallbackModel)
	}
	if err := s.rt.SetLLMConfig(cfg); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

// TestLLM probes the OpenAI-compatible endpoint (e.g. https://api.openai.com/v1).
func (s *AppService) TestLLM() APITestResult {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return TestLLMConnection(ctx, s.rt.LLMConfig())
}

// ListPendingApprovals returns approval cards for the chat UI.
func (s *AppService) ListPendingApprovals() []ApprovalDTO {
	ctx := context.Background()
	pending, err := s.rt.Approvals().ListPending(ctx, nil)
	if err != nil {
		return nil
	}
	out := make([]ApprovalDTO, 0, len(pending))
	for _, p := range pending {
		dto := ApprovalDTO{
			ID:      p.ID,
			Summary: p.Summary,
			Prompt:  p.Prompt,
			Status:  p.Status,
		}
		if p.Metadata != nil {
			if v, ok := p.Metadata["tool_name"].(string); ok {
				dto.ToolName = v
			}
			if v, ok := p.Metadata["arguments"].(string); ok {
				dto.Arguments = v
			}
		}
		out = append(out, dto)
	}
	return out
}

// ResolveApproval approves or rejects a pending request, then resumes ReAct if approved.
func (s *AppService) ResolveApproval(approvalID string, approved bool, note string, overrideArgs ...string) map[string]any {
	ctx := context.Background()
	var finalArgs string
	if len(overrideArgs) > 0 {
		finalArgs = overrideArgs[0]
	}
	resume := &governance.ResumePayload{Approved: approved, Arguments: finalArgs}
	if err := s.rt.Approvals().Resolve(ctx, approvalID, approved, note, "desktop_user", resume); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if s.app != nil {
		s.app.Event.Emit("approval:resolved", map[string]any{
			"approval_id": approvalID,
			"approved":    approved,
			"arguments":   finalArgs,
		})
	}

	out := map[string]any{"success": true}
	if approved {
		if pr, ok := s.rt.pending.Get(approvalID); ok {
			if finalArgs != "" {
				pr.Arguments = finalArgs
			}
			if pr.ResumeKind == "workflow" {
				wfOut, err := s.rt.resumeWorkflowAfterApproval(ctx, pr, resume)
				s.rt.pending.Delete(approvalID)
				if err != nil {
					out["resume_error"] = err.Error()
				} else {
					out["workflow_output"] = wfOut
					if s.app != nil {
						s.app.Event.Emit("workflow:resumed", map[string]any{
							"approval_id": approvalID,
							"workflow_id": pr.WorkflowID,
							"output":      wfOut,
						})
					}
				}
				return out
			}
			runner := s.rt.AgentRunner()
			var runRes *agent.RunResult
			var err error
			if runner != nil && pr.InterruptID != "" {
				if pr.ResumeKind == "matrix" {
					runRes, err = runner.ResumeMatrixSupervisor(ctx, s.rt.AgentLLMSettings(), pr.InterruptID, resume)
				} else {
					runRes, err = runner.ContinueAfterApproval(ctx, s.rt.AgentLLMSettings(), pr.SessionID, pr.InterruptID, pr.ToolName, pr.Arguments, true)
				}
			}
			s.rt.pending.Delete(approvalID)
			if err != nil {
				out["resume_error"] = err.Error()
			} else if runRes != nil {
				content := runRes.Content
				msgs := []ChatMessageDTO{{Role: "assistant", Type: "text", Content: content}}
				if pr.ResumeKind == "matrix" {
					ev := capability.Event{Type: "matrix.resumed", Source: pr.MatrixEvent}
					s.rt.publishMatrixOrchestrated(ev, content, "")
					if s.app != nil {
						s.app.Event.Emit("matrix:resumed", map[string]any{
							"approval_id": approvalID, "content": content,
						})
					}
				} else if pr.SessionID != "" {
					_ = s.rt.Sessions().AppendMessage(ctx, pr.SessionID, "assistant", content, "text", nil)
				}
				out["messages"] = msgs
				if s.app != nil && pr.ResumeKind != "matrix" {
					s.app.Event.Emit("chat:done", map[string]any{
						"session_id": pr.SessionID,
						"messages":   msgs,
						"resume":     true,
					})
				}
			}
		}
	} else {
		if pr, ok := s.rt.pending.Get(approvalID); ok && pr.ResumeKind == "workflow" {
			_, _ = s.rt.resumeWorkflowAfterApproval(ctx, pr, resume)
			s.rt.pending.Delete(approvalID)
		} else {
			s.rt.pending.Delete(approvalID)
		}
	}
	return out
}

// SendMessage runs workspace + memory + ReAct/LLM.
func (s *AppService) SendMessage(userText string) SendMessageResult {
	return s.sendMessageCore(context.Background(), "desktop", strings.TrimSpace(userText), nil)
}

// OpenWorkflowWindow opens a new independent desktop window for the Workflow Flowgram editor.
func (s *AppService) OpenWorkflowWindow(workflowID string) map[string]any {
	q := url.Values{}
	q.Set("view", "workflow")
	if strings.TrimSpace(workflowID) != "" {
		q.Set("workflow_id", strings.TrimSpace(workflowID))
	}
	title := "Workflow Editor"
	if strings.TrimSpace(workflowID) != "" {
		title += " - " + strings.TrimSpace(workflowID)
	}
	return s.openUtilityWindow("workflow-editor", title, "/?"+q.Encode(), 1120, 780, 820, 560)
}

// OpenInnerAppWindow opens an InnerApp UI bundle in an independent desktop window.
func (s *AppService) OpenInnerAppWindow(name string) map[string]any {
	name = strings.TrimSpace(name)
	if name == "" {
		return map[string]any{"success": false, "error": "app name required"}
	}
	if s.rt.appStore == nil {
		return map[string]any{"success": false, "error": "app store unavailable"}
	}
	app, err := s.rt.appStore.GetByName(context.Background(), name)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if app.Kind != "ui" && strings.TrimSpace(app.BundlePath) == "" {
		return map[string]any{"success": false, "error": "inner app has no UI bundle"}
	}

	q := url.Values{}
	q.Set("view", "innerapp")
	q.Set("app", app.Name)
	return s.openUtilityWindow("innerapp-"+safeWindowName(app.Name), "Inner App - "+app.Name, "/?"+q.Encode(), 980, 720, 640, 420)
}

func (s *AppService) openUtilityWindow(name, title, windowURL string, width, height, minWidth, minHeight int) map[string]any {
	if s.app == nil {
		return map[string]any{"success": false, "error": "app not initialized"}
	}
	if win, ok := s.app.Window.GetByName(name); ok {
		win.SetURL(windowURL).Show()
		win.Focus()
		return map[string]any{"success": true, "window": name, "reused": true}
	}

	s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      name,
		Title:     title,
		Width:     width,
		Height:    height,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		URL:       windowURL,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	return map[string]any{"success": true, "window": name}
}

func safeWindowName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

func memoryRecordFromTurn(user, assistant string) memory.Record {
	now := time.Now().Unix()
	return memory.Record{
		ID:        fmt.Sprintf("turn_%d", now),
		Content:   fmt.Sprintf("User: %s\nAssistant: %s", user, assistant),
		Scope:     "session",
		Modality:  "episode",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
