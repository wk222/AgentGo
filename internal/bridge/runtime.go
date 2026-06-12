package bridge

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/admin"
	"agentgo/internal/agent"
	"agentgo/internal/agentpack"
	"agentgo/internal/applog"
	"agentgo/internal/apps"
	"agentgo/internal/capability"
	"agentgo/internal/checkpoint"
	"agentgo/internal/gateway"
	"agentgo/internal/governance"
	"agentgo/internal/kanban"
	"agentgo/internal/memory"
	"agentgo/internal/scheduler"
	"agentgo/internal/sessions"
	"agentgo/internal/skills"
	"agentgo/internal/taskhub"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"agentgo/internal/workspace"
)

type Runtime struct {
	mu               sync.RWMutex
	dataDir          string
	workspace        string
	llm              LLMConfig
	governanceCfg    GovernanceConfig
	memStore         *memory.SQLiteStore
	mem              memory.Engine
	approvals        *governance.ApprovalQueue
	wsMiddleware     *workspace.ContextMiddleware
	sessions         *sessions.Store
	agentRunner      *agent.Runner
	toolReg          *tools.Registry
	capBus           *capability.Bus
	kanban           *kanban.Store
	pending          *pendingStore
	taskHub          *taskhub.Hub
	sched            *scheduler.Store
	schedRunner      *scheduler.Runner
	skillLoader      *skills.Loader
	wfStore          *workflow.Store
	cpStore          *checkpoint.SQLiteStore
	wfExec           func(ctx context.Context, workflowID, input string) (string, error)
	wfResume         func(ctx context.Context, workflowID, checkPointID, interruptID, resumeJSON string) (string, error)
	gatewaySrv       *gateway.Server
	gatewayBroker    *gateway.Broker
	distillScheduler *memory.DistillScheduler
	appStore         *apps.Store
	innerAppSessions map[string]innerAppSession
	appsRoot         string
	adminRunner      *admin.AdminRunner
	interactStore    *tools.InteractionStore
	runTrack         *RunTracker
	agentPack        *agentpack.Engine
}

func NewRuntime() (*Runtime, error) {
	dataDir, err := xdg.DataFile("agentgo")
	if err != nil {
		dataDir = filepath.Join(".", "data")
	} else {
		dataDir = filepath.Dir(dataDir)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := applog.Init(dataDir); err != nil {
		log.Printf("[agentgo] file log disabled: %v", err)
	}
	cfg, govCfg, wsCfg := loadAppConfig(dataDir)
	log.Printf("[agentgo] data_dir=%s db=%s governance=%s", dataDir, filepath.Join(dataDir, "agentgo.db"), govCfg.ControlMode)

	dbPath := filepath.Join(dataDir, "agentgo.db")
	memStore, err := memory.NewSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	memCfg := memory.BootConfigFromEnv(cfg.APIBase, cfg.APIKey)
	hybrid, err := memory.NewHybridEngine(context.Background(), memStore, memCfg)
	if err != nil {
		return nil, err
	}
	pipeline := memory.NewEnrichedEngine(hybrid)

	queue, err := governance.NewApprovalQueueWithDB(memStore.DB())
	if err != nil {
		return nil, err
	}
	sessStore, err := sessions.Open(memStore.DB())
	if err != nil {
		return nil, err
	}

	wsRoot := strings.TrimSpace(os.Getenv("AGENTGO_WORKSPACE_ROOT"))
	if wsRoot == "" {
		wsRoot = strings.TrimSpace(wsCfg.Root)
	}
	if wsRoot == "" {
		wsRoot, err = os.Getwd()
		if err != nil {
			wsRoot = dataDir
		}
	}
	if normalized, err := normalizeWorkspaceRoot(wsRoot); err == nil {
		wsRoot = normalized
	} else {
		log.Printf("[agentgo] workspace_root=%q invalid: %v; falling back to data dir", wsRoot, err)
		wsRoot = dataDir
	}

	policy := governance.BuildPolicy(govCfg.ControlMode, wsRoot)
	cpStore, err := checkpoint.OpenSQLiteStore(memStore.DB())
	if err != nil {
		return nil, err
	}
	governance.StartApprovalEscalation(context.Background(), queue, time.Minute, 30*time.Minute)

	capStore, _ := capability.NewStore(memStore.DB())
	capBus := capability.NewBusWithStore(capStore)
	capBus.SeedDefaults()

	wfStore, _ := workflow.NewStore(memStore.DB())
	_ = wfStore.EnsureHappyPathTemplate()
	skillLoader := skills.NewLoader(wsRoot)
	skillLoader.Reload()

	toolReg := tools.NewRegistry()
	if err := tools.Bootstrap(context.Background(), toolReg, wsRoot); err != nil {
		return nil, err
	}
	dynStore, _ := tools.NewDynamicStore(memStore.DB())
	sandboxDir := filepath.Join(dataDir, "sandbox")
	toolReg.SetDynamicSandbox(sandboxDir)

	wsMW := workspace.NewContextMiddleware(wsRoot)
	agentRunner := agent.NewRunner(pipeline, wsMW, queue, policy, toolReg, cpStore, dataDir, wsRoot, capBus)
	if subReg, err := agent.NewSubagentRegistry(memStore.DB()); err == nil {
		agentRunner.SetSubagentRegistry(subReg)
		_, _ = subReg.SeedDefaults(context.Background())
	}

	interactStore := tools.NewInteractionStore()

	rt := &Runtime{
		dataDir: dataDir, workspace: wsRoot, llm: cfg, governanceCfg: govCfg,
		memStore: memStore, mem: pipeline, approvals: queue,
		wsMiddleware: wsMW, sessions: sessStore, toolReg: toolReg,
		agentRunner: agentRunner, capBus: capBus, cpStore: cpStore,
		kanban: nil, pending: newPendingStore(),
		skillLoader: skillLoader, wfStore: wfStore,
		interactStore: interactStore,
		runTrack:      NewRunTracker(),
	}

	// === 初始化 AdminRunner (持久化任务) ===
	adminStore, err := admin.NewStore(memStore.DB())
	if err != nil {
		return nil, err
	}
	adminRunner := admin.NewAdminRunner(adminStore, agentRunner)
	rt.adminRunner = adminRunner
	adminRunner.SubscribeRuntimeHeal(capBus)

	agentRunner.SetLLMProvider(func() agent.LLMSettings {
		c := rt.LLMConfig()
		return agent.LLMSettings{APIBase: c.APIBase, APIKey: c.APIKey, Model: c.Model, FallbackModel: c.FallbackModel}
	})
	agentRunner.SetAdminRunner(adminRunner)
	agentRunner.SetSessionsStore(sessStore)
	// 启动后台管理员任务轮询，10秒检查一次
	adminRunner.Start(context.Background(), 10*time.Second)

	rt.wfExec = rt.executeWorkflow
	rt.wfResume = rt.resumeWorkflow

	wfSave := func(name, description, nodesJSON string) (string, error) {
		def, err := wfStore.SaveFromRegister(name, description, nodesJSON)
		if err != nil {
			return "", err
		}
		return def.ID, nil
	}
	capSynth := capability.NewSynthesizePipeline(capBus)
	onCompiled := func(def tools.DynamicToolDef) error {
		if err := toolReg.RegisterDynamicTool(def); err != nil {
			return err
		}
		_, _ = capSynth.CompileAndRegister(context.Background(), capability.SynthesizeRequest{
			Kind: "tool", Name: def.Name, Scope: "agent", Source: "dynamic_compile",
		})
		return nil
	}
	onApp := func(name, mode, appID string) {
		capBus.RegisterAppMatrixGrant(name, mode, appID)
	}
	_ = tools.RegisterPyBotModeTools(toolReg, dynStore, wsRoot, dataDir, func(kind, name, scope string) {
		capBus.Register(kind, name, scope, nil)
	}, onCompiled, onApp, wfSave)
	_ = toolReg.SyncDynamicFromStore(dynStore)
	if strings.TrimSpace(os.Getenv("AGENTGO_LEGACY_DYNAMIC_EXEC")) == "1" {
		_ = tools.RegisterDynamicPythonExec(toolReg, dynStore, sandboxDir)
	}
	_ = capability.NewMatrixCoordinator(capBus, rt.onCapabilityEvent)
	appsRoot := filepath.Join(dataDir, "apps")
	_ = os.MkdirAll(appsRoot, 0o755)
	_ = apps.EnsureDemoApp(appsRoot)
	rt.appsRoot = appsRoot
	appStore, _ := apps.NewStore(memStore.DB())
	rt.appStore = appStore
	_, _ = apps.ScanRoots(context.Background(), appStore, appsRoot, filepath.Join(wsRoot, "apps"))
	_ = tools.RegisterMatrixOrchestrationTools(toolReg, appStore, &matrixRunnerAdapter{rt: rt})
	_ = tools.RegisterMatrixAliasTools(toolReg, appStore, &matrixRunnerAdapter{rt: rt})
	onInnerApp := func(a apps.InnerApp) {
		capBus.Register("app", a.Name, "inner", map[string]string{
			"kind": a.Kind, "app_id": a.ID,
		})
	}
	_ = tools.RegisterInnerAppTools(toolReg, appStore, rt, onInnerApp)
	scaffolder := apps.NewScaffolder(appsRoot, appStore)
	_ = tools.RegisterInnerAppScaffoldTools(toolReg, &tools.InnerAppScaffold{
		Scaffolder: scaffolder,
		OnUpsert:   onInnerApp,
	})
	iterBuilder := &apps.IterativeBuilder{Scaffolder: scaffolder, Pinger: rt.appPinger()}
	_ = agent.RegisterAppBuilderTools(toolReg, agentRunner, iterBuilder, func() agent.LLMSettings {
		c := rt.LLMConfig()
		return agent.LLMSettings{APIBase: c.APIBase, APIKey: c.APIKey, Model: c.Model, FallbackModel: c.FallbackModel}
	}, agent.SessionIDFromContext, onInnerApp)
	_ = tools.RegisterWorkflowTools(toolReg, wfStore, rt)
	agentPackEngine := &agentpack.Engine{
		WF:        wfStore,
		Dyn:       dynStore,
		Apps:      appStore,
		Reg:       toolReg,
		AppsRoot:  appsRoot,
		Workspace: wsRoot,
		OutDir:    filepath.Join(dataDir, "shared"),
	}
	rt.agentPack = agentPackEngine
	_ = agentpack.RegisterTools(toolReg, agentPackEngine)
	_ = tools.RegisterRememberTool(toolReg, func(ctx context.Context, rec memory.Record) error {
		return pipeline.Ingest(ctx, rec)
	})
	_ = tools.RegisterRecallTool(toolReg, func(ctx context.Context, query string, opts memory.RecallOptions) ([]memory.Record, error) {
		if opts.Scope == "" {
			opts.Scope = agent.SessionIDFromContext(ctx)
		}
		return pipeline.Recall(ctx, query, opts)
	})
	_ = tools.RegisterA2UITool(toolReg, rt.interactStore, func(ctx context.Context, ev tools.A2UIRenderEvent) {
		sessionID := agent.SessionIDFromContext(ctx)
		applog.A2UI("render_ui session=%s component=%s data_bytes=%d interact=%s",
			sessionID, ev.Component, len(ev.DataJSON), ev.InteractID)
		if sessionID == "" {
			applog.Warn("render_ui without session_id in context — UI 事件将无法按会话过滤")
		}
		if rt.gatewayBroker != nil {
			// Broadcast the A2UI event over the gateway
			rt.gatewayBroker.Publish("a2ui", "render", tools.MarshalRenderEventJSON(ev))
		}
		if app := application.Get(); app != nil {
			payload := tools.MarshalRenderEvent(ev)
			if sessionID != "" {
				payload["session_id"] = sessionID
			}
			app.Event.Emit("a2ui:render", payload)
		}
		if ev.InteractID != "" && sessionID != "" {
			if app := application.Get(); app != nil {
				app.Event.Emit("chat:paused", map[string]any{
					"session_id":  sessionID,
					"interact_id": ev.InteractID,
					"reason":      "a2ui_interaction",
				})
			}
		}
		if sessionID != "" {
			meta := map[string]any{
				"component":   ev.Component,
				"data_json":   ev.DataJSON,
				"interact_id": ev.InteractID,
				"surface":     ev.Surface,
			}
			_ = rt.Sessions().AppendMessage(ctx, sessionID, "assistant", "", "aui", meta)
		}
	})
	_ = agent.RegisterSubagentTool(toolReg, agentRunner, func() agent.LLMSettings {
		c := rt.LLMConfig()
		return agent.LLMSettings{APIBase: c.APIBase, APIKey: c.APIKey, Model: c.Model, FallbackModel: c.FallbackModel}
	}, agent.SessionIDFromContext)
	_ = registerActivateSkillOnRegistry(toolReg, skillLoader)

	kanbanStore, err := kanban.Open(memStore.DB())
	if err != nil {
		return nil, err
	}
	rt.kanban = kanbanStore

	broker := gateway.NewBroker()
	rt.gatewayBroker = broker

	taskHub, err := taskhub.New(memStore.DB())
	if err != nil {
		return nil, err
	}
	taskHub.SetRunner(rt.runBackgroundTask)
	taskHub.AddEmitter(func(taskID string, ev taskhub.Event) {
		broker.Publish(taskID, ev.Type, mustJSON(ev))
	})
	rt.taskHub = taskHub

	gwBackend := NewRuntimeGateway(rt, broker)
	addr := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_ADDR"))
	if addr == "" {
		if p := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_PORT")); p != "" {
			addr = "127.0.0.1:" + p
		}
	}
	if addr != "" {
		rt.gatewaySrv = gateway.NewServer(gateway.Config{
			Addr:   addr,
			Token:  strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_TOKEN")),
			Broker: broker,
		}, gwBackend)
		go func() {
			if err := rt.gatewaySrv.Start(); err != nil && !strings.Contains(err.Error(), "closed") {
				log.Printf("[gateway] %v", err)
			}
		}()
	}

	schedStore, err := scheduler.NewStore(memStore.DB())
	if err != nil {
		return nil, err
	}
	rt.sched = schedStore
	rt.schedRunner = scheduler.NewRunner(schedStore, func(ctx context.Context, job scheduler.Job) {
		_, _ = taskHub.Start("cron", job.SessionID, job.Prompt)
	})
	rt.schedRunner.Start()

	rt.distillScheduler = memory.NewDistillScheduler(hybrid.Pipeline(),
		func() string {
			if ar := rt.agentRunner; ar != nil {
				return ar.SessionMode().MemoryDistillScope()
			}
			return "session"
		},
		func() int {
			if ar := rt.agentRunner; ar != nil {
				return ar.SessionMode().DistillIntervalHours()
			}
			return 24
		},
	)
	rt.distillScheduler.Start(context.Background())

	journalCaller := func(ctx context.Context, system, user string) (string, error) {
		cfg := rt.LLMConfig()
		if cfg.APIKey == "" {
			return "", nil
		}
		return ChatOnce(ctx, cfg, system, user)
	}
	agentRunner.SetJournalCaller(journalCaller)
	if pipe := hybrid.Pipeline(); pipe != nil {
		agentRunner.SetEpisodicCompressor(memory.NewEpisodicCompressor(pipe, journalCaller))
	}

	// Perform startup synchronization of all assets/capabilities into CapabilityBus
	go rt.syncAllCapabilities(context.Background())

	adminRunner.SetPlanner(admin.NewLLMPlanner(journalCaller))

	return rt, nil
}

func registerActivateSkillOnRegistry(r *tools.Registry, loader *skills.Loader) error {
	return tools.RegisterActivateSkill(r, loader)
}

// Run implements tools.WorkflowRunner.
func (r *Runtime) Run(ctx context.Context, workflowID, input string) (string, error) {
	return r.executeWorkflow(ctx, workflowID, input)
}

// SyncWorkflowTools rebuilds workflow-as-tool first-class tools.
func (r *Runtime) SyncWorkflowTools() error {
	if r.toolReg == nil || r.wfStore == nil {
		return nil
	}
	return tools.SyncAllWorkflowTools(r.toolReg, r.wfStore, r, r.workflowRunContextPtr)
}

func (r *Runtime) DataDir() string { return r.dataDir }

// CreateAdminDurableTask enqueues a multi-step admin goal (PersistentAdminRunner).
func (r *Runtime) CreateAdminDurableTask(ctx context.Context, goal string) (*admin.DurableTask, error) {
	if r.adminRunner == nil {
		return nil, fmt.Errorf("admin runner unavailable")
	}
	return r.adminRunner.EnqueueTask(ctx, goal)
}

func (r *Runtime) LLMConfig() LLMConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.llm
}

func (r *Runtime) SetLLMConfig(cfg LLMConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.llm = cfg
	return saveAppConfig(r.dataDir, cfg, r.governanceCfg, WorkspaceConfig{Root: r.workspace})
}

func (r *Runtime) GovernanceConfig() GovernanceConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.governanceCfg
}

func (r *Runtime) SetGovernanceControlMode(mode string) error {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "balanced"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.governanceCfg.ControlMode = mode
	policy := governance.BuildPolicy(mode, r.workspace)
	if r.agentRunner != nil {
		r.agentRunner.SetPolicy(policy)
	}
	return saveAppConfig(r.dataDir, r.llm, r.governanceCfg, WorkspaceConfig{Root: r.workspace})
}

func (r *Runtime) GovernancePolicySnapshot() map[string]any {
	r.mu.RLock()
	ws := r.workspace
	mode := r.governanceCfg.ControlMode
	r.mu.RUnlock()
	if mode == "" {
		mode = "balanced"
	}
	p := governance.BuildPolicy(mode, ws)
	snap := p.Control.ToMap()
	snap["pipeline_stages"] = governance.BuildDefaultToolPolicyPipeline(p, governance.NewToolCallTracker()).Describe()
	snap["workspace_root"] = ws
	return snap
}

func (r *Runtime) Memory() memory.Engine { return r.mem }

func (r *Runtime) memoryPipeline() *memory.Pipeline {
	if r == nil {
		return nil
	}
	switch m := r.mem.(type) {
	case *memory.EnrichedEngine:
		return m.Pipeline()
	case *memory.HybridEngine:
		return m.Pipeline()
	case *memory.Pipeline:
		return m
	default:
		return nil
	}
}
func (r *Runtime) Approvals() *governance.ApprovalQueue              { return r.approvals }
func (r *Runtime) WorkspaceMiddleware() *workspace.ContextMiddleware { return r.wsMiddleware }
func (r *Runtime) Sessions() *sessions.Store                         { return r.sessions }
func (r *Runtime) AgentRunner() *agent.Runner                        { return r.agentRunner }
func (r *Runtime) CapabilityBus() *capability.Bus                    { return r.capBus }
func (r *Runtime) ToolRegistry() *tools.Registry                     { return r.toolReg }
func (r *Runtime) WorkspaceRoot() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workspace
}
func (r *Runtime) Kanban() *kanban.Store              { return r.kanban }
func (r *Runtime) A2UIStore() *tools.InteractionStore { return r.interactStore }

func (r *Runtime) syncAllCapabilities(ctx context.Context) {
	if r.capBus == nil {
		return
	}

	// 1. Sync static tools from Registry
	if r.toolReg != nil {
		var syncTools []capability.SyncToolDTO
		for _, t := range r.toolReg.GetAllTools() {
			info, err := t.Info(ctx)
			if err == nil && info != nil {
				syncTools = append(syncTools, capability.SyncToolDTO{
					Name:        info.Name,
					Description: info.Desc,
					Scope:       "agent",
				})
			}
		}
		r.capBus.SyncTools(syncTools)
	}

	// 2. Sync workflows from Store
	if r.wfStore != nil {
		wfs, _ := r.wfStore.List()
		var syncWfs []capability.SyncWorkflowDTO
		for _, w := range wfs {
			syncWfs = append(syncWfs, capability.SyncWorkflowDTO{
				ID:          w.ID,
				Name:        w.Name,
				Description: w.Description,
			})
		}
		r.capBus.SyncWorkflows(syncWfs)
	}

	// 3. Sync inner apps from Store
	if r.appStore != nil {
		innerApps, _ := r.appStore.List(ctx, 100)
		var syncApps []capability.SyncAppDTO
		for _, app := range innerApps {
			syncApps = append(syncApps, capability.SyncAppDTO{
				ID:          app.ID,
				Name:        app.Name,
				Description: app.Description,
				Kind:        app.Kind,
			})
		}
		r.capBus.SyncApps(syncApps)
	}

	// 4. Sync sub-agents from SubagentRegistry
	if r.agentRunner != nil && r.agentRunner.SubagentRegistry() != nil {
		subagents, _ := r.agentRunner.SubagentRegistry().List(ctx, 100)
		var syncAgents []capability.SyncAgentDTO
		for _, sub := range subagents {
			syncAgents = append(syncAgents, capability.SyncAgentDTO{
				ID:           sub.ID,
				Name:         sub.Name,
				Role:         sub.Role,
				SystemPrompt: sub.SystemPrompt,
			})
		}
		r.capBus.SyncAgents(syncAgents)
	}
}
