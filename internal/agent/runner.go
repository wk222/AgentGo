package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"

	"agentgo/internal/admin"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/tools"
	"agentgo/internal/workspace"
)

// defaultSystemPrompt is the base persona used when no override is configured.
const defaultSystemPrompt = "You are AgentGo, a capable desktop AI assistant. Use tools when helpful. For UI cards and system metrics (CPU, memory, health), call render_ui with component metric or card. Be concise."

// baseSystemPrompt returns the configurable base system prompt.
// Override the persona via the AGENTGO_SYSTEM_PROMPT environment variable;
// otherwise it falls back to defaultSystemPrompt. Per-run mode guidance is
// layered on separately via SessionMode.ModeHints (see mode_middleware.go).
func baseSystemPrompt() string {
	if v := strings.TrimSpace(os.Getenv("AGENTGO_SYSTEM_PROMPT")); v != "" {
		return v
	}
	return defaultSystemPrompt
}

// Runner wires ADK ChatModelAgent (preferred) or react fallback + memory/workspace.
type Runner struct {
	memMW              *MemoryMiddleware
	wsMW               *workspace.ContextMiddleware
	modeMu             sync.RWMutex
	defaultSessionMode SessionMode
	sessionModes       map[string]SessionMode
	queue              *governance.ApprovalQueue
	policy             governance.Policy
	toolReg            *tools.Registry
	cpStore            adk.CheckPointStore
	dataDir            string
	workspaceRoot      string
	runControl         *RunControl
	adminRunner        *admin.AdminRunner
	llmProvider        func() LLMSettings
	capBus             *capability.Bus
	episodicCompressor *memory.EpisodicCompressor
	subagentReg        *SubagentRegistry
}

func NewRunner(mem memory.Engine, ws *workspace.ContextMiddleware, queue *governance.ApprovalQueue, policy governance.Policy, toolReg *tools.Registry, cpStore adk.CheckPointStore, dataDir, workspaceRoot string, capBus *capability.Bus) *Runner {
	sm := EnvSessionMode()
	r := &Runner{
		memMW:              NewMemoryMiddleware(mem),
		wsMW:               ws,
		defaultSessionMode: sm,
		sessionModes:       make(map[string]SessionMode),
		queue:              queue,
		policy:             policy,
		toolReg:            toolReg,
		cpStore:            cpStore,
		dataDir:            dataDir,
		workspaceRoot:      workspaceRoot,
		runControl:         NewRunControl(),
		capBus:             capBus,
	}
	return r
}

// SetAdminRunner sets the AdminRunner for the SessionSpineMiddleware task injection.
func (r *Runner) SetAdminRunner(ar *admin.AdminRunner) {
	r.adminRunner = ar
}

// SetLLMProvider sets the provider for dynamic LLM configurations used by background tasks.
func (r *Runner) SetLLMProvider(provider func() LLMSettings) {
	r.llmProvider = provider
}

// SetEpisodicCompressor wires MemoryDistill compaction into SessionSpine.
func (r *Runner) SetEpisodicCompressor(c *memory.EpisodicCompressor) {
	if r != nil {
		r.episodicCompressor = c
	}
}

// CancelSessionRun stops the in-flight ADK run for a session (desktop stop / gateway).
func (r *Runner) CancelSessionRun(sessionID string) bool {
	if r == nil || r.runControl == nil {
		return false
	}
	return r.runControl.CancelSession(sessionID)
}

// SetSessionMode updates PyBot ModeProfile × ExecutionCanvas for subsequent runs.
func (r *Runner) SetSessionMode(m SessionMode) {
	r.SetDefaultSessionMode(m)
}

func (r *Runner) SetDefaultSessionMode(m SessionMode) {
	if r == nil {
		return
	}
	r.modeMu.Lock()
	r.defaultSessionMode = m.Normalized()
	r.modeMu.Unlock()
}

func (r *Runner) SetSessionModeForSession(sessionID string, m SessionMode) {
	if r == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		r.SetDefaultSessionMode(m)
		return
	}
	r.modeMu.Lock()
	if r.sessionModes == nil {
		r.sessionModes = make(map[string]SessionMode)
	}
	r.sessionModes[sessionID] = m.Normalized()
	r.modeMu.Unlock()
}

func (r *Runner) ClearSessionModeForSession(sessionID string) {
	if r == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.modeMu.Lock()
	delete(r.sessionModes, sessionID)
	r.modeMu.Unlock()
}

// SetJournalCaller wires LLM for AGENTGO_AUTO_JOURNAL post-turn diary (MemoryDistill JOURNAL).
func (r *Runner) SetJournalCaller(caller memory.LLMCaller) {
	if r != nil && r.memMW != nil {
		r.memMW.SetJournalCaller(caller)
	}
}

func (r *Runner) SessionMode() SessionMode {
	if r == nil {
		return EnvSessionMode()
	}
	r.modeMu.RLock()
	sm := r.defaultSessionMode
	r.modeMu.RUnlock()
	if sm.Profile == "" && sm.Canvas == "" {
		return EnvSessionMode()
	}
	return sm.Normalized()
}

func (r *Runner) SessionModeForSession(sessionID string) SessionMode {
	if r == nil {
		return EnvSessionMode()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		r.modeMu.RLock()
		sm, ok := r.sessionModes[sessionID]
		r.modeMu.RUnlock()
		if ok {
			return sm.Normalized()
		}
	}
	return r.SessionMode()
}

func (r *Runner) modeForRun(ctx context.Context, sessionID string) SessionMode {
	if sm, ok := sessionModeFromContext(ctx); ok {
		return sm
	}
	return r.SessionModeForSession(sessionID)
}

func (r *Runner) withSessionMode(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return WithSessionMode(ctx, r.modeForRun(ctx, sessionID))
}

func (r *Runner) maxIterationsFor(ctx context.Context) int {
	return SessionModeFromContext(ctx).MaxIterations()
}

// EffectivePolicy returns governance policy including dynamic compiled tools as HIGH risk.
func (r *Runner) EffectivePolicy() governance.Policy {
	return r.effectivePolicy()
}

// SetPolicy replaces the base governance policy (e.g. after control-mode change in settings).
func (r *Runner) SetPolicy(policy governance.Policy) {
	r.policy = policy
}

// SetWorkspaceRoot keeps the runner's prompt middleware and governance policy
// aligned with the desktop workspace selected by the UI.
func (r *Runner) SetWorkspaceRoot(root string, ws *workspace.ContextMiddleware, policy governance.Policy) {
	if r == nil {
		return
	}
	r.workspaceRoot = root
	r.wsMW = ws
	r.SetPolicy(policy)
}

func (r *Runner) effectivePolicy() governance.Policy {
	p := r.policy
	p.Control = r.policy.Control
	p.WorkspaceRoot = r.policy.WorkspaceRoot
	p.ToolRiskLevels = copyRiskLevels(r.policy.ToolRiskLevels)
	p.BlockedTools = copyBoolMap(r.policy.BlockedTools)
	if p.ToolRiskLevels == nil {
		p.ToolRiskLevels = make(map[string]governance.RiskLevel)
	}
	if r.toolReg != nil {
		for _, name := range r.toolReg.DynamicCompiledNames() {
			if name != "" {
				p.ToolRiskLevels[name] = governance.RiskHigh
			}
		}
	}
	p.ToolRiskLevels["execute_dynamic_tool"] = governance.RiskHigh
	return p
}

func (r *Runner) effectivePolicyForCtx(ctx context.Context) governance.Policy {
	p := r.effectivePolicy()
	canvasPolicy := CanvasPolicyFromContext(ctx)
	if canvasPolicy.MaxIterations > 0 {
		if canvasPolicy.StuckLoopThreshold > 0 {
			p.Control.StuckLoopKillThreshold = canvasPolicy.StuckLoopThreshold
			p.Control.StuckLoopWarningThreshold = canvasPolicy.StuckLoopThreshold / 2
			if p.Control.StuckLoopWarningThreshold < 2 {
				p.Control.StuckLoopWarningThreshold = 2
			}
		}
		if canvasPolicy.AgentDepth > 0 {
			p.Control.MaxSubagentDepth = canvasPolicy.AgentDepth
		}
		if canvasPolicy.MaxIterations > 0 {
			p.Control.MaxCallsPerTool = canvasPolicy.MaxIterations * 2
		}
	}
	return p
}

func copyRiskLevels(in map[string]governance.RiskLevel) map[string]governance.RiskLevel {
	if in == nil {
		return nil
	}
	out := make(map[string]governance.RiskLevel, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBoolMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// LLMSettings mirrors bridge.LLMConfig without import cycle.
type LLMSettings struct {
	APIBase       string
	APIKey        string
	Model         string
	FallbackModel string // optional Model Failover target
}

// Generate runs a single-turn ReAct loop.
func (r *Runner) Generate(ctx context.Context, cfg LLMSettings, sessionID, userText string) (*RunResult, error) {
	return r.run(ctx, cfg, sessionID, userText, nil)
}

// GenerateStream streams assistant text deltas via emit, then returns the final RunResult.
func (r *Runner) GenerateStream(ctx context.Context, cfg LLMSettings, sessionID, userText string, emit func(delta string)) (*RunResult, error) {
	return r.run(ctx, cfg, sessionID, userText, emit)
}

func (r *Runner) attachTraceCallbacks(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(traceEmitKey{}).(func(TraceRecord)); !ok {
		return ctx
	}
	return callbacks.InitCallbacks(ctx, &callbacks.RunInfo{
		Name: "agentgo_run", Type: "Runner", Component: components.ComponentOfChatModel,
	}, NewTraceCallbackHandler(r.capBus))
}

func (r *Runner) run(ctx context.Context, cfg LLMSettings, sessionID, userText string, emit func(string)) (*RunResult, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("未配置 API Key")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	EmitTrace(runCtx, "start", "Runner", "run", "开始处理")

	if r.runControl != nil {
		cleanSessionID := strings.TrimPrefix(sessionID, "agentic:")
		r.runControl.SetCtxCancel(cleanSessionID, cancel)
		defer r.runControl.Clear(cleanSessionID)
	}

	EmitTrace(runCtx, "start", "Runner", "adk", "ADK 对话环")
	res, err := r.runADK(runCtx, cfg, sessionID, userText, emit)
	if err == nil {
		EmitTrace(runCtx, "end", "Runner", "adk", "")
		return r.maybeContinue(runCtx, cfg, sessionID, res, emit)
	}
	EmitTrace(runCtx, "error", "Runner", "adk", err.Error())
	return nil, err
}

func (r *Runner) maybeContinue(ctx context.Context, cfg LLMSettings, sessionID string, res *RunResult, emit func(string)) (*RunResult, error) {
	if res == nil || res.PendingApproval != nil {
		return res, nil
	}
	content := res.Content
	for i := 0; i < maxAutoContinue && NeedsContinuation(content, ""); i++ {
		cont, err := r.Generate(ctx, cfg, sessionID, RepairContinuationPrompt(content))
		if err != nil || cont == nil || cont.Content == "" {
			break
		}
		content = MergeContinuation(content, cont.Content)
		if emit != nil {
			emit(cont.Content)
		}
	}
	res.Content = content
	return res, nil
}

// MatrixCoordinatorSession is the ADK checkpoint session for App Matrix supervisor runs.
const MatrixCoordinatorSession = "app_matrix_coordinator"

// ContinueAfterApproval resumes via checkpoint interrupt when possible, else re-prompts ReAct.
func (r *Runner) ContinueAfterApproval(ctx context.Context, cfg LLMSettings, sessionID, interruptID, toolName, arguments string, approved bool) (*RunResult, error) {
	payload := &governance.ResumePayload{Approved: approved, Arguments: arguments}
	if interruptID != "" && r.cpStore != nil {
		if sessionID == MatrixCoordinatorSession {
			return r.ResumeMatrixSupervisor(ctx, cfg, interruptID, payload)
		}
		return r.ResumeInterrupt(ctx, cfg, sessionID, interruptID, payload)
	}
	userText := fmt.Sprintf(
		"[系统] 用户已批准执行高风险工具 %s，参数为 %s。请立即调用该工具完成任务，并简要汇报 stdout/stderr。",
		toolName, arguments,
	)
	return r.Generate(ctx, cfg, sessionID, userText)
}

// ToolNames returns registered tool names for debugging UI.
func (r *Runner) ToolNames(ctx context.Context) []string {
	tools := r.toolReg.GetAllTools()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err == nil && info != nil {
			names = append(names, info.Name)
		}
	}
	return names
}

// MiddlewareNames documents the chain (maps to PyBot middleware stack conceptually).
func MiddlewareNames() []string {
	return []string{
		"mode_profile",
		"workspace_context",
		"memory_inject",
		"memory_ingest",
		"governance_compose_tool_wrap",
		"summarization",
		"agentsmd",
		"reduction",
		"patch_tool_calls",
		"eino_skill_mw",
		"checkpoint_resume",
		"tool_search",
		"truncation_continue",
		"taskhub_sse",
		"ask_user",
		"invoke_subagent",
		"agentic_memory",
		"agentic_workspace",
		"agentic_message",
		"loop_guard",
		"capability_bus",
	}
}

// Ensure tool package is referenced when registry empty at compile time.
var _ tool.BaseTool
