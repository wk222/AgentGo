package bridge

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"agentgo/internal/admin"
	"agentgo/internal/agent"
	"agentgo/internal/agentpack"
	"agentgo/internal/applog"
	"agentgo/internal/apps"
	"agentgo/internal/capability"
	"agentgo/internal/checkpoint"
	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/sessions"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"agentgo/internal/workspace"
)

func (rt *Runtime) agentPlugins() []plugin.Plugin {
	return []plugin.Plugin{
		// The agent consumes memory, approvals, checkpoints, tools and the rest
		// through the host, so replacing one of those plugins needs no change here.
		corePlugin("agent", []string{svcAgent},
			[]string{svcMemory, svcModel, svcApprovals, svcCheckpoints, svcTools, svcCapability, svcWorkspace, svcSessions, svcDB}, nil,
			func(c *plugin.Context) error {
				mem, err := plugin.Use[memory.Engine](c, svcMemory)
				if err != nil {
					return err
				}
				model, err := plugin.Use[ModelSource](c, svcModel)
				if err != nil {
					return err
				}
				queue, err := plugin.Use[*governance.ApprovalQueue](c, svcApprovals)
				if err != nil {
					return err
				}
				cp, err := plugin.Use[*checkpoint.SQLiteStore](c, svcCheckpoints)
				if err != nil {
					return err
				}
				reg, err := plugin.Use[*tools.Registry](c, svcTools)
				if err != nil {
					return err
				}
				bus, err := plugin.Use[*capability.Bus](c, svcCapability)
				if err != nil {
					return err
				}
				wsMW, err := plugin.Use[*workspace.ContextMiddleware](c, svcWorkspace)
				if err != nil {
					return err
				}
				sess, err := plugin.Use[*sessions.Store](c, svcSessions)
				if err != nil {
					return err
				}
				db, err := plugin.Use[*sql.DB](c, svcDB)
				if err != nil {
					return err
				}

				runner := agent.NewRunner(mem, wsMW, queue, rt.policy, reg, cp, rt.dataDir, rt.workspace, bus)
				c.Effect("runner-close", func() { _ = runner.Close() })
				if subReg, err := agent.NewSubagentRegistry(db); err == nil {
					runner.SetSubagentRegistry(subReg)
					_, _ = subReg.SeedDefaults(context.Background())
				}
				rt.agentRunner = runner

				// Durable background tasks.
				adminStore, err := admin.NewStore(db)
				if err != nil {
					return err
				}
				adminRunner := admin.NewAdminRunner(adminStore, runner)
				rt.adminRunner = adminRunner
				adminRunner.SubscribeRuntimeHeal(bus)

				runner.SetLLMProvider(llmSettingsFrom(model))
				runner.SetAdminRunner(adminRunner)
				runner.SetSessionsStore(sess)

				ctx := scopedContext(c)
				adminRunner.Start(ctx, 10*time.Second)
				c.Effect("stop admin runner", adminRunner.Stop)

				journalCaller := func(ctx context.Context, system, user string) (string, error) {
					cfg := model.LLM()
					if cfg.APIKey == "" {
						return "", nil
					}
					return ChatOnce(ctx, cfg, system, user)
				}
				runner.SetJournalCaller(journalCaller)
				if tracer := runner.UsageTracer(); tracer != nil {
					// Calls made outside the agent loop (memory upkeep, workflow nodes, inline edits) count too.
					c.Effect("chat-usage-sink", setChatUsageSink(func(m string, prompt, cached, completion, reasoning int, took time.Duration) {
						tracer.Record(m, "", prompt, cached, completion, reasoning, took)
					}))
				}
				if rt.hybrid != nil {
					if pipe := rt.hybrid.Pipeline(); pipe != nil {
						runner.SetEpisodicCompressor(memory.NewEpisodicCompressor(pipe, journalCaller))
					}
				}
				adminRunner.SetPlanner(admin.NewLLMPlanner(journalCaller))
				return c.Provide(svcAgent, runner)
			}),

		corePlugin("workflow", []string{svcWorkflow}, []string{svcDB}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			store, err := workflow.NewStore(db)
			if err != nil {
				return err
			}
			_ = store.EnsureHappyPathTemplate()
			rt.wfStore = store
			rt.wfExec = rt.executeWorkflow
			rt.wfResume = rt.resumeWorkflow
			return c.Provide(svcWorkflow, store)
		}),

		corePlugin("apps", []string{svcApps}, []string{svcDB, svcWorkspace}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			appsRoot := filepath.Join(rt.dataDir, "apps")
			_ = os.MkdirAll(appsRoot, 0o755)
			_ = apps.EnsureDemoApp(appsRoot)
			rt.appsRoot = appsRoot
			store, err := apps.NewStore(db)
			if err != nil {
				return err
			}
			rt.appStore = store
			_, _ = apps.ScanRoots(context.Background(), store, appsRoot, filepath.Join(rt.workspace, "apps"))
			return c.Provide(svcApps, store)
		}),

		// The built-in tools the agent can call. Apps, memory, sessions and the
		// capability bus are optional here: a tool whose store is missing is
		// registered against it as before, so an absent app store does not take
		// the other tools down.
		corePlugin("toolset", []string{svcToolset},
			[]string{svcTools, svcAgent, svcWorkflow, svcModel},
			[]string{svcApps, svcMemory, svcSessions, svcCapability, svcDB},
			func(c *plugin.Context) error {
				model, err := plugin.Use[ModelSource](c, svcModel)
				if err != nil {
					return err
				}
				reg, err := plugin.Use[*tools.Registry](c, svcTools)
				if err != nil {
					return err
				}
				runner, err := plugin.Use[*agent.Runner](c, svcAgent)
				if err != nil {
					return err
				}
				wfStore, err := plugin.Use[*workflow.Store](c, svcWorkflow)
				if err != nil {
					return err
				}
				bus, err := plugin.Use[*capability.Bus](c, svcCapability)
				if err != nil {
					return err
				}
				mem, err := plugin.Use[memory.Engine](c, svcMemory)
				if err != nil {
					return err
				}
				return rt.registerBuiltinTools(c, reg, runner, wfStore, bus, mem, llmSettingsFrom(model))
			}),
	}
}

// registerBuiltinTools registers the agent's built-in tool families into reg.
func (rt *Runtime) registerBuiltinTools(c *plugin.Context, reg *tools.Registry, runner *agent.Runner,
	wfStore *workflow.Store, bus *capability.Bus, mem memory.Engine, llm func() agent.LLMSettings) error {
	wsRoot := rt.workspace
	sandboxDir := filepath.Join(rt.dataDir, "sandbox")

	wfSave := func(name, description, nodesJSON string) (string, error) {
		def, err := wfStore.SaveFromRegister(name, description, nodesJSON)
		if err != nil {
			return "", err
		}
		return def.ID, nil
	}
	capSynth := capability.NewSynthesizePipeline(bus)
	onCompiled := func(def tools.DynamicToolDef) error {
		if err := reg.RegisterDynamicTool(def); err != nil {
			return err
		}
		_, _ = capSynth.CompileAndRegister(context.Background(), capability.SynthesizeRequest{
			Kind: "tool", Name: def.Name, Scope: "agent", Source: "dynamic_compile",
		})
		return nil
	}
	onApp := func(name, mode, appID string) {
		bus.RegisterAppMatrixGrant(name, mode, appID)
	}
	// Tool-family registration failures must not vanish into `_ =`: the app
	// would start with capabilities silently missing and no trace anywhere.
	// Collect them, log each one, and print a summary count at the end.
	var regErrs []string
	noteReg := func(what string, err error) {
		if err != nil {
			regErrs = append(regErrs, what+": "+err.Error())
			applog.Warn("tool registration failed: %s: %v", what, err)
		}
	}
	noteReg("RegisterPyBotModeTools", tools.RegisterPyBotModeTools(reg, rt.dynStore, wsRoot, rt.dataDir, func(kind, name, scope string) {
		bus.Register(kind, name, scope, nil)
	}, onCompiled, onApp, wfSave))
	noteReg("SyncDynamicFromStore", reg.SyncDynamicFromStore(rt.dynStore))
	if config.LegacyDynExec.On() {
		noteReg("RegisterDynamicPythonExec", tools.RegisterDynamicPythonExec(reg, rt.dynStore, sandboxDir))
	}
	_ = capability.NewMatrixCoordinator(bus, rt.onCapabilityEvent)

	appStore := rt.appStore
	noteReg("RegisterMatrixOrchestrationTools", tools.RegisterMatrixOrchestrationTools(reg, appStore, &matrixRunnerAdapter{rt: rt}))
	noteReg("RegisterMatrixAliasTools", tools.RegisterMatrixAliasTools(reg, appStore, &matrixRunnerAdapter{rt: rt}))
	onInnerApp := func(a apps.InnerApp) {
		bus.Register("app", a.Name, "inner", map[string]string{
			"kind": a.Kind, "app_id": a.ID,
		})
	}
	noteReg("RegisterInnerAppTools", tools.RegisterInnerAppTools(reg, appStore, rt, onInnerApp))
	scaffolder := apps.NewScaffolder(rt.appsRoot, appStore)
	noteReg("RegisterInnerAppScaffoldTools", tools.RegisterInnerAppScaffoldTools(reg, &tools.InnerAppScaffold{
		Scaffolder: scaffolder,
		OnUpsert:   onInnerApp,
	}))
	iterBuilder := &apps.IterativeBuilder{Scaffolder: scaffolder, Pinger: rt.appPinger()}
	noteReg("RegisterAppBuilderTools", agent.RegisterAppBuilderTools(reg, runner, iterBuilder, llm, agent.SessionIDFromContext, onInnerApp))
	noteReg("RegisterWorkflowTools", tools.RegisterWorkflowTools(reg, wfStore, rt))
	agentPackEngine := &agentpack.Engine{
		WF:          wfStore,
		Dyn:         rt.dynStore,
		Apps:        appStore,
		Reg:         reg,
		AppsRoot:    rt.appsRoot,
		Workspace:   wsRoot,
		OutDir:      filepath.Join(rt.dataDir, "shared"),
		VersionsDir: filepath.Join(rt.dataDir, "extension-versions"),
		OnReload: func() error {
			if rt.skillLoader != nil {
				rt.skillLoader.Reload()
			}
			return rt.SyncWorkflowTools()
		},
	}
	rt.agentPack = agentPackEngine
	noteReg("agentpack.RegisterTools", agentpack.RegisterTools(reg, agentPackEngine))
	if err := agentpack.RegisterEvolutionTools(reg, agentPackEngine); err != nil {
		return err
	}
	if err := tools.RegisterSkillEvolveTool(reg, wsRoot); err != nil {
		return err
	}
	for _, declaration := range []struct {
		name     string
		mutating bool
	}{{"extension_catalog", false}, {"manage_extension_versions", true}, {"save_workflow_definition", true}} {
		c.Effect("extension-static-"+declaration.name, tools.RegisterStaticTool(declaration.name, declaration.mutating))
		c.Effect("extension-traits-"+declaration.name, governance.RegisterToolTraits(declaration.name, governance.ToolTraits{Source: "extensions", Mutating: declaration.mutating}))
	}

	// Compaction keeps a path ledger; which tools change files is declared above that package.
	c.Effect("sessions-file-mutation", sessions.SetFileMutationLookup(func(name string) bool {
		t, ok := governance.ToolTraitsOf(name)
		return ok && t.Mutating
	}))

	noteReg("RegisterRememberTool", tools.RegisterRememberTool(reg, func(ctx context.Context, rec memory.Record) error {
		return mem.Ingest(ctx, rec)
	}))
	if pipe := memory.PipelineFromEngine(mem); pipe != nil {
		noteReg("RegisterDocumentLearningTools", tools.RegisterDocumentLearningTools(reg, pipe, agent.SessionIDFromContext, rt.WorkspaceRoot))
		c.Effect("learning-neighbors-static", tools.RegisterStaticTool("read_learned_neighbors", false))
		c.Effect("learning-neighbors-traits", governance.RegisterToolTraits("read_learned_neighbors", governance.ToolTraits{Source: "memory", Mutating: false}))
		c.Effect("learning-document-static", tools.RegisterStaticTool("learn_document", true))
		c.Effect("learning-document-traits", governance.RegisterToolTraits("learn_document", governance.ToolTraits{Source: "memory", Mutating: true}))
		c.Effect("learning-status-static", tools.RegisterStaticTool("memory_learning_status", false))
		c.Effect("learning-status-traits", governance.RegisterToolTraits("memory_learning_status", governance.ToolTraits{Source: "memory", Mutating: false}))
	}
	noteReg("RegisterRecallTool", tools.RegisterRecallTool(reg, func(ctx context.Context, query string, opts memory.RecallOptions) ([]memory.Record, error) {
		if opts.Scope == "" {
			opts.Scope = agent.SessionIDFromContext(ctx)
		}
		return mem.Recall(ctx, query, opts)
	}))
	noteReg("RegisterA2UITool", tools.RegisterA2UITool(reg, rt.interactStore, rt.renderA2UI))
	noteReg("RegisterSubagentTool", agent.RegisterSubagentTool(reg, runner, llm, agent.SessionIDFromContext))
	noteReg("registerActivateSkillOnRegistry", registerActivateSkillOnRegistry(reg, rt.skillLoader))
	if len(regErrs) > 0 {
		applog.Warn("tool registration: %d registration(s) failed at startup: %v", len(regErrs), regErrs)
	}
	return c.Provide(svcToolset, true)
}

// renderA2UI shows an interactive UI the agent asked for: it is broadcast to
// the gateway and the desktop window, and stored in the session.
func (rt *Runtime) renderA2UI(ctx context.Context, ev tools.A2UIRenderEvent) {
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
	if eventsOn() {
		payload := tools.MarshalRenderEvent(ev)
		if sessionID != "" {
			payload["session_id"] = sessionID
		}
		emitEvent("a2ui:render", payload)
	}
	if ev.InteractID != "" && sessionID != "" {
		if eventsOn() {
			emitEvent("chat:paused", map[string]any{
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
}
