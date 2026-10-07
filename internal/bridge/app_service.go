package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ct "github.com/charmbracelet/crush/pkg/codetools"
	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/engine"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/taskhub"
)

// AppService exposes AgentGo backend to the Wails frontend via IPC.
type AppService struct {
	rt              *Runtime
	app             *application.App
	engines         *engine.Router
	plugins         *plugin.Host
	desktopEventSeq atomic.Int64

	muTools     sync.RWMutex
	codetools   *ct.Toolbox
	lspListener func(e ct.LSPEvent)

	unbindWorkspace func() // removes this service's binder from the Runtime
}

func (s *AppService) setCodetools(tb *ct.Toolbox) {
	s.muTools.Lock()
	s.codetools = tb
	listener := s.lspListener
	if tb != nil && listener != nil {
		tb.SetLSPEventListener(listener)
	}
	s.muTools.Unlock()
}

func (s *AppService) getCodetools() *ct.Toolbox {
	s.muTools.RLock()
	defer s.muTools.RUnlock()
	return s.codetools
}

func (s *AppService) setLSPListener(fn func(e ct.LSPEvent)) {
	s.muTools.Lock()
	s.lspListener = fn
	if s.codetools != nil && fn != nil {
		s.codetools.SetLSPEventListener(fn)
	}
	s.muTools.Unlock()
}

func NewAppService(rt *Runtime) *AppService {
	s := &AppService{rt: rt, engines: engine.NewRouter()}
	s.initPlugins() // eino (default engine) and, when installed, crush
	return s
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
	// Degraded is set when the agent run failed and the reply came from a
	// tool-less chat instead. The text is not a result of the task: callers
	// that act on a reply (quick fix, automation) must not treat it as done.
	Degraded string `json:"degraded,omitempty"`
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
	azKey, azEp := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	hasAzureLuna := azKey != "" && azEp != ""
	azureBase := ""
	if hasAzureLuna {
		azureBase = strings.TrimRight(azEp, "/") + "/openai/v1/"
	}
	azureModel := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if azureModel == "" {
		azureModel = "gpt-6-luna"
	}
	return map[string]any{
		"api_base":       cfg.APIBase,
		"api_key_set":    cfg.APIKey != "",
		"api_key_hint":   masked,
		"model":          cfg.Model,
		"fallback_model": cfg.FallbackModel,
		"has_azure_luna": hasAzureLuna,
		"azure_api_base": azureBase,
		"azure_model":    azureModel,
	}
}

// LoadAzureLunaConfig switches the current LLM configuration to the local Azure Luna preset.
func (s *AppService) LoadAzureLunaConfig() map[string]any {
	azKey, azEp := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	if azKey == "" || azEp == "" {
		return map[string]any{"success": false, "error": "本地环境中未检测到 AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT"}
	}
	model := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if model == "" {
		model = "gpt-6-luna"
	}
	cfg := LLMConfig{
		APIBase: strings.TrimRight(azEp, "/") + "/openai/v1/",
		APIKey:  azKey,
		Model:   model,
	}
	if err := s.rt.SetLLMConfig(cfg); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
		_ = os.Setenv("AGENTGO_REASONING_EFFORT", "none")
	}
	return map[string]any{"success": true, "model": model, "api_base": cfg.APIBase}
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

// ResolveApproval records the user's decision on a pending request and, when
// approved, resumes the paused run. The work is done by resolveApproval; this is
// the desktop binding.
func (s *AppService) ResolveApproval(approvalID string, approved bool, note string, overrideArgs ...string) map[string]any {
	var finalArgs string
	if len(overrideArgs) > 0 {
		finalArgs = overrideArgs[0]
	}
	return s.resolveApproval(context.Background(), approvalID, approved, note, "desktop_user", finalArgs)
}

// SendMessage runs workspace + memory + ReAct/LLM.
func (s *AppService) SendMessage(userText string) SendMessageResult {
	return s.sendMessageCore(context.Background(), "desktop", strings.TrimSpace(userText), nil, nil)
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
