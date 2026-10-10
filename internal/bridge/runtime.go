package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"


	"agentgo/internal/admin"
	"agentgo/internal/agent"
	"agentgo/internal/agentpack"
	"agentgo/internal/apps"
	"agentgo/internal/capability"
	"agentgo/internal/checkpoint"
	"agentgo/internal/gateway"
	"agentgo/internal/trigger"
	"agentgo/internal/governance"
	"agentgo/internal/ideruntime"
	"agentgo/internal/kanban"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/scheduler"
	"agentgo/internal/sessions"
	"agentgo/internal/skills"
	"agentgo/internal/taskhub"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"agentgo/internal/engine"
	"agentgo/internal/workspace"
)

type Runtime struct {
	engineRouter     *engine.Router // set by AppService; backs the CodingAgent workflow node
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
	relay            approvalRelay // decisions on approvals, for surfaces waiting on another surface
	taskHub          *taskhub.Hub
	sched            *scheduler.Store
	schedRunner      *scheduler.Runner
	triggers         *trigger.Engine
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
	workspaceReviews *workspaceReviewStore
	supervisor       *ideruntime.ProcessSupervisor
	workspaceFS      *ideruntime.WorkspaceFS
	terminalSvc      *ideruntime.TerminalService
	searchSvc        *ideruntime.SearchService
	gitSvc           *ideruntime.GitService

	// host owns the runtime's services (see runtime_boot.go). Nil for a Runtime
	// assembled by hand in a test.
	host *plugin.Host
	// Set by the runtime plugins and shared through the host.
	hybrid        *memory.HybridEngine
	dynStore      *tools.DynamicStore
	policy        governance.Policy
	workspaceHint string // workspace root from config.json, used when the env does not set one

	// resumeFn replaces the agent resume in tests; nil in production.
	resumeFn func(ctx context.Context, pr pendingRun, resume *governance.ResumePayload) (*agent.RunResult, error)

	bindMu       sync.Mutex // guards workspaceBinders (see workspace_runtime.go)
	wsBinders    []workspaceBinderEntry
	nextBinderID int
}


func (r *Runtime) Supervisor() *ideruntime.ProcessSupervisor       { return r.supervisor }
func (r *Runtime) WorkspaceFS() *ideruntime.WorkspaceFS             { return r.workspaceFS }
func (r *Runtime) TerminalService() *ideruntime.TerminalService     { return r.terminalSvc }
func (r *Runtime) SearchService() *ideruntime.SearchService         { return r.searchSvc }
func (r *Runtime) GitService() *ideruntime.GitService               { return r.gitSvc }

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

// LLMConfig is the model configuration now in force. It asks the host's model
// service, so a replaced "model" plugin is honoured by every caller; a runtime
// without a host (tests) falls back to the configured value.
func (r *Runtime) LLMConfig() LLMConfig {
	if r.host != nil {
		if v, ok := r.host.Service(svcModel); ok {
			if m, ok := v.(ModelSource); ok {
				return m.LLM()
			}
		}
	}
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
