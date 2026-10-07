package bridge

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/capability"
	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/sessions"
	"agentgo/internal/skills"
	"agentgo/internal/tools"
	"agentgo/internal/workspace"
)

// corePlugin is a plugin the runtime cannot work without: users cannot stop it,
// and a failure to start it fails NewRuntime.
func corePlugin(name string, provides, inject, optional []string, apply func(*plugin.Context) error) plugin.Plugin {
	return plugin.Plugin{Name: name, Provides: provides, Inject: inject, Optional: optional, Apply: apply, Protected: true}
}

// optionalPlugin is a feature that may be absent, disabled by configuration,
// or fail without taking the runtime down.
func optionalPlugin(name string, provides, inject, optional []string, apply func(*plugin.Context) error) plugin.Plugin {
	return plugin.Plugin{Name: name, Provides: provides, Inject: inject, Optional: optional, Apply: apply}
}

// runtimePlugins lists the runtime's plugins. Order here is only a tie-break;
// the host starts them by their declared dependencies.
func (rt *Runtime) runtimePlugins() []plugin.Plugin {
	var ps []plugin.Plugin
	ps = append(ps, rt.dataPlugins()...)
	ps = append(ps, rt.agentPlugins()...)
	ps = append(ps, rt.featurePlugins()...)
	return ps
}

// scopedContext is a context cancelled when the plugin stops.
func scopedContext(c *plugin.Context) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	c.Effect("cancel context", cancel)
	return ctx
}

func (rt *Runtime) dataPlugins() []plugin.Plugin {
	return []plugin.Plugin{
		corePlugin("storage", []string{svcDB}, nil, nil, func(c *plugin.Context) error {
			memStore, err := memory.NewSQLiteStore(filepath.Join(rt.dataDir, "agentgo.db"))
			if err != nil {
				return err
			}
			rt.memStore = memStore
			c.Effect("close database", func() { _ = memStore.DB().Close() })
			return c.Provide(svcDB, memStore.DB())
		}),

		// Which model to talk to. Nothing to start; it exists so that the model
		// is a named, replaceable service like the others.
		corePlugin("model", []string{svcModel}, nil, nil, func(c *plugin.Context) error {
			return c.Provide(svcModel, ModelSource(configModel{rt: rt}))
		}),

		corePlugin("memory", []string{svcMemory}, []string{svcDB, svcModel}, nil, func(c *plugin.Context) error {
			src, err := plugin.Use[ModelSource](c, svcModel)
			if err != nil {
				return err
			}
			llm := src.LLM()
			memCfg := memory.BootConfigFromEnv(llm.APIBase, llm.APIKey)
			hybrid, err := memory.NewHybridEngine(scopedContext(c), rt.memStore, memCfg)
			if err != nil {
				return err
			}
			pipeline := memory.NewEnrichedEngine(hybrid)
			rt.hybrid, rt.mem = hybrid, pipeline
			return c.Provide(svcMemory, memory.Engine(pipeline))
		}),

		corePlugin("sessions", []string{svcSessions}, []string{svcDB}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			st, err := sessions.Open(db)
			if err != nil {
				return err
			}
			rt.sessions = st
			return c.Provide(svcSessions, st)
		}),

		corePlugin("checkpoints", []string{svcCheckpoints}, []string{svcDB}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			cp, err := checkpoint.OpenSQLiteStore(db)
			if err != nil {
				return err
			}
			rt.cpStore = cp
			return c.Provide(svcCheckpoints, cp)
		}),

		// Approvals also own what happens to runs that wait for them: the
		// pending-run store, picking up runs a dead process left, and letting go
		// of runs whose approval expires. Checkpoints are optional only so that
		// they start first; recovery reads them.
		corePlugin("approvals", []string{svcApprovals}, []string{svcDB, svcSessions}, []string{svcCheckpoints}, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			queue, err := governance.NewApprovalQueueWithDB(db)
			if err != nil {
				return err
			}
			rt.approvals = queue
			rt.pending = newDurablePendingStore(rt.sessions)

			ctx := scopedContext(c)
			rt.RecoverPending(ctx)
			governance.StartApprovalEscalationNotify(ctx, queue, time.Minute, 30*time.Minute, func(int64) {
				rt.sweepExpiredPending(ctx)
			})
			return c.Provide(svcApprovals, queue)
		}),

		corePlugin("capability", []string{svcCapability}, []string{svcDB}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			capStore, err := capability.NewStore(db)
			if err != nil {
				log.Printf("[runtime] capability store unavailable, grants are not persisted: %v", err)
			}
			bus := capability.NewBusWithStore(capStore)
			bus.SeedDefaults()
			rt.capBus = bus
			return c.Provide(svcCapability, bus)
		}),

		corePlugin("workspace", []string{svcWorkspace}, nil, nil, func(c *plugin.Context) error {
			wsRoot := strings.TrimSpace(os.Getenv("AGENTGO_WORKSPACE_ROOT"))
			if wsRoot == "" {
				wsRoot = strings.TrimSpace(rt.workspaceHint)
			}
			if wsRoot == "" {
				var err error
				if wsRoot, err = os.Getwd(); err != nil {
					wsRoot = rt.dataDir
				}
			}
			if normalized, err := normalizeWorkspaceRoot(wsRoot); err == nil {
				wsRoot = normalized
			} else {
				log.Printf("[agentgo] workspace_root=%q invalid: %v; falling back to data dir", wsRoot, err)
				wsRoot = rt.dataDir
			}
			rt.workspace = wsRoot
			rt.policy = governance.BuildPolicy(rt.governanceCfg.ControlMode, wsRoot)
			rt.wsMiddleware = workspace.NewContextMiddleware(wsRoot)
			rt.skillLoader = skills.NewLoader(wsRoot)
			rt.skillLoader.Reload()
			return c.Provide(svcWorkspace, rt.wsMiddleware)
		}),

		corePlugin("tools", []string{svcTools}, []string{svcDB, svcWorkspace}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			reg := tools.NewRegistry()
			if err := tools.Bootstrap(context.Background(), reg, rt.workspace); err != nil {
				return err
			}
			dyn, err := tools.NewDynamicStore(db)
			if err != nil {
				log.Printf("[runtime] dynamic tool store unavailable: %v", err)
			}
			reg.SetDynamicSandbox(filepath.Join(rt.dataDir, "sandbox"))
			rt.toolReg, rt.dynStore = reg, dyn
			return c.Provide(svcTools, reg)
		}),
	}
}
