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

	"github.com/google/uuid"

	"agentgo/internal/coderuntime"
	"agentgo/internal/config"
	"agentgo/internal/engine"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/taskhub"
)

// AppService exposes AgentGo backend to the Wails frontend via IPC.
type AppService struct {
	rt              *Runtime
	app             desktopApp // Wails host in desktop builds; unused in the "web" build
	plugins         *plugin.Host
	desktopEventSeq atomic.Int64

	muTools     sync.RWMutex
	lspListener func(e coderuntime.LSPEvent)

	unbindWorkspace func() // removes this service's binder from the Runtime
}

func (s *AppService) setCodetools(tb coderuntime.Toolbox) {
	s.muTools.Lock()
	if s.rt != nil {
		s.rt.mu.Lock()
		s.rt.codetools = tb
		s.rt.mu.Unlock()
	}
	listener := s.lspListener
	if tb != nil && listener != nil {
		tb.SetLSPEventListener(listener)
	}
	s.muTools.Unlock()
}

func (s *AppService) getCodetools() coderuntime.Toolbox {
	if s.rt == nil {
		return nil
	}
	return s.rt.codeToolbox()
}

func (s *AppService) setLSPListener(fn func(e coderuntime.LSPEvent)) {
	s.muTools.Lock()
	s.lspListener = fn
	if tb := s.getCodetools(); tb != nil && fn != nil {
		tb.SetLSPEventListener(fn)
	}
	s.muTools.Unlock()
}

func NewAppService(rt *Runtime) *AppService {
	if rt == nil {
		rt = &Runtime{}
	}
	rt.mu.Lock()
	if rt.engineRouter == nil {
		rt.engineRouter = engine.NewRouter()
	}
	rt.mu.Unlock()
	s := &AppService{rt: rt}
	s.initPlugins() // desktop extensions join the runtime's native engine
	return s
}

// startTaskEvents forwards background task events to the UI event stream.
// Desktop builds call it from ServiceStartup, the web shell calls it directly.
func (s *AppService) startTaskEvents() {
	if s.rt.taskHub != nil {
		s.rt.taskHub.AddEmitter(func(taskID string, ev taskhub.Event) {
			if s.eventsOn() {
				s.emit("task:event", map[string]any{
					"task_id": taskID, "seq": ev.Seq, "type": ev.Type, "payload": ev.Payload,
				})
			}
		})
	}
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
	azureModel := config.AzureDeployment.Get()
	if azureModel == "" {
		azureModel = "gpt-6-luna"
	}
	dsKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	hasDeepSeek := dsKey != ""
	return map[string]any{
		"api_base":       cfg.APIBase,
		"api_key_set":    cfg.APIKey != "",
		"api_key_hint":   masked,
		"model":          cfg.Model,
		"fallback_model": cfg.FallbackModel,
		"has_azure_luna": hasAzureLuna,
		"azure_api_base": azureBase,
		"azure_model":    azureModel,
		"has_deepseek":   hasDeepSeek,
	}
}

// LoadDeepSeekConfig switches the current LLM configuration to the DeepSeek preset (preset).
func (s *AppService) LoadDeepSeekConfig(model string) map[string]any {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "deepseek-reasoner"
	}
	fallback := "deepseek-chat"
	if model == "deepseek-chat" {
		fallback = "deepseek-reasoner"
	}
	cfg := s.rt.LLMConfig()
	cfg.ContextWindow, cfg.MaxOutputTokens = 0, 0
	cfg.APIBase = "https://api.deepseek.com/v1"
	cfg.Model = model
	cfg.FallbackModel = fallback
	if dsKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); dsKey != "" {
		cfg.APIKey = dsKey
	}
	if err := s.rt.SetLLMConfig(cfg); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success":      true,
		"model":        cfg.Model,
		"api_base":     cfg.APIBase,
		"api_key_set":  cfg.APIKey != "",
		"from_env_key": os.Getenv("DEEPSEEK_API_KEY") != "",
	}
}

// LoadAzureLunaConfig switches the current LLM configuration to the local Azure Luna preset.
func (s *AppService) LoadAzureLunaConfig() map[string]any {
	azKey, azEp := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	if azKey == "" || azEp == "" {
		return map[string]any{"success": false, "error": "本地环境中未检测到 AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT"}
	}
	model := config.AzureDeployment.Get()
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
	if !config.ReasoningEffort.IsSet() {
		config.ReasoningEffort.Set("none")
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
	if cfg.Model != s.rt.LLMConfig().Model || cfg.APIBase != s.rt.LLMConfig().APIBase {
		cfg.ContextWindow, cfg.MaxOutputTokens = 0, 0
	}
	cfg.FallbackModel = strings.TrimSpace(fallbackModel)
	if err := s.rt.SetLLMConfig(cfg); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

// TestLLM probes the OpenAI-compatible endpoint (e.g. https://api.openai.com/v1).
func (s *AppService) TestLLM(ctx context.Context) APITestResult {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
	return s.SendMessageWithSession("desktop", strings.TrimSpace(userText), nil)
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
		ID:        "turn_" + uuid.NewString(),
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
