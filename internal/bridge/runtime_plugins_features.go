package bridge

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/applog"
	"agentgo/internal/gateway"
	"agentgo/internal/tools"
	"agentgo/internal/trigger"
	"agentgo/internal/ideruntime"
	"agentgo/internal/kanban"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/scheduler"
	"agentgo/internal/taskhub"
)

// newDistillScheduler builds the periodic memory distillation job. Its scope
// and cadence follow the agent's session mode, read each time it runs.
func newDistillScheduler(rt *Runtime) *memory.DistillScheduler {
	return memory.NewDistillScheduler(rt.hybrid.Pipeline(),
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
}

func (rt *Runtime) featurePlugins() []plugin.Plugin {
	return []plugin.Plugin{
		corePlugin("kanban", nil, []string{svcDB}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			store, err := kanban.Open(db)
			if err != nil {
				return err
			}
			rt.kanban = store
			return nil
		}),

		corePlugin("taskhub", []string{svcTaskHub}, []string{svcDB, svcAgent}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			broker := gateway.NewBroker()
			hub, err := taskhub.New(db)
			if err != nil {
				return err
			}
			hub.SetRunner(rt.runBackgroundTask)
			hub.AddEmitter(func(taskID string, ev taskhub.Event) {
				broker.Publish(taskID, ev.Type, mustJSON(ev))
			})
			rt.gatewayBroker, rt.taskHub = broker, hub
			return c.Provide(svcTaskHub, hub)
		}),

		// The HTTP gateway only runs when an address is configured.
		optionalPlugin("gateway", nil, []string{svcTaskHub}, nil, func(c *plugin.Context) error {
			addr := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_ADDR"))
			if addr == "" {
				if p := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_PORT")); p != "" {
					addr = "127.0.0.1:" + p
				}
			}
			if addr == "" {
				return nil
			}
			srv := gateway.NewServer(gateway.Config{
				Addr:   addr,
				Token:  strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_TOKEN")),
				Broker: rt.gatewayBroker,
			}, NewRuntimeGateway(rt, rt.gatewayBroker))
			rt.gatewaySrv = srv
			done := make(chan struct{})
			go func() {
				defer close(done)
				if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) && !strings.Contains(err.Error(), "closed") {
					log.Printf("[gateway] %v", err)
				}
			}()
			c.Effect("stop gateway", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
				select {
				case <-done:
				case <-time.After(3 * time.Second):
				}
			})
			return nil
		}),

		corePlugin("scheduler", nil, []string{svcDB, svcTaskHub}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			hub, err := plugin.Use[*taskhub.Hub](c, svcTaskHub)
			if err != nil {
				return err
			}
			store, err := scheduler.NewStore(db)
			if err != nil {
				return err
			}
			rt.sched = store
			runner := scheduler.NewRunner(store, func(ctx context.Context, job scheduler.Job) {
				_, _ = hub.Start("cron", job.SessionID, job.Prompt)
			})
			rt.schedRunner = runner
			// Headless hosts (MCP/Crush sidecar) set AGENTGO_DISABLE_SCHEDULER=1 so cron
			// jobs are not fired twice when the desktop app shares the same data dir.
			if os.Getenv("AGENTGO_DISABLE_SCHEDULER") != "1" {
				runner.Start()
				c.Effect("stop scheduler", runner.Stop)
			}
			return nil
		}),

		// Event triggers (process exit / marker file / log match / webhook) that wake an agent session.
		// Persisted in SQLite, so they survive restarts; AGENTGO_DISABLE_TRIGGERS=1 turns the watchers off
		// in a second process sharing the same data dir.
		optionalPlugin("triggers", nil, []string{svcDB, svcTaskHub}, nil, func(c *plugin.Context) error {
			db, err := plugin.Use[*sql.DB](c, svcDB)
			if err != nil {
				return err
			}
			store, err := trigger.NewStore(db)
			if err != nil {
				return err
			}
			engine := trigger.NewEngine(store, rt.fireTrigger)
			rt.triggers = engine
			tools.SetWatchHandler(rt) // the agent's watch_event tool arms triggers through this
			c.Effect("unset watch handler", func() { tools.SetWatchHandler(nil) })
			if os.Getenv("AGENTGO_DISABLE_TRIGGERS") == "1" {
				return nil
			}
			if err := engine.Start(scopedContext(c)); err != nil {
				return err
			}
			c.Effect("stop triggers", engine.Stop)
			return nil
		}),

		optionalPlugin("distill", nil, []string{svcMemory, svcAgent}, nil, func(c *plugin.Context) error {
			if rt.hybrid == nil || rt.hybrid.Pipeline() == nil {
				return nil // the memory in use has nothing to distill
			}
			rt.distillScheduler = newDistillScheduler(rt)
			rt.distillScheduler.Start(scopedContext(c))
			return nil
		}),

		// Registers every asset known at startup with the capability bus, off
		// the startup path. It is last: the optional dependencies only order it
		// after the plugins that contribute assets.
		optionalPlugin("capability-sync", nil, []string{svcCapability},
			[]string{svcToolset, svcApps, svcWorkflow, svcIDE}, func(c *plugin.Context) error {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					defer close(done)
					rt.syncAllCapabilities(ctx)
				}()
				c.Effect("stop capability sync", func() {
					cancel()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						log.Printf("[runtime] capability sync did not stop within 3s")
					}
				})
				return nil
			}),

		// IDE foundations: process supervision, the workspace file system and
		// its watcher, terminals, search and git.
		optionalPlugin("ide", []string{svcIDE}, []string{svcWorkspace}, nil, func(c *plugin.Context) error {
			supervisor, supErr := ideruntime.NewProcessSupervisor()
			if supErr != nil {
				applog.Warn("ProcessSupervisor init failed: %v", supErr)
			} else {
				rt.supervisor = supervisor
				c.Effect("close process supervisor", func() { _ = supervisor.Close() })
			}

			workspaceFS, fsErr := ideruntime.NewWorkspaceFS(rt.workspace)
			if fsErr != nil {
				applog.Warn("WorkspaceFS init failed: %v", fsErr)
			} else {
				rt.workspaceFS = workspaceFS
				_ = workspaceFS.StartWatcher()
				c.Effect("stop file watcher", workspaceFS.StopWatcher)
				workspaceFS.OnFilesChanged(func(batch ideruntime.FSChangeBatch) {
					if app := application.Get(); app != nil && app.Event != nil {
						app.Event.Emit("workspace:filesChanged", batch)
					}
				})
			}

			if rt.supervisor != nil {
				termSvc := ideruntime.NewTerminalService(rt.supervisor)
				rt.terminalSvc = termSvc
				termSvc.OnTerminalData(func(termID, data string) {
					if app := application.Get(); app != nil && app.Event != nil {
						app.Event.Emit("terminal:data", map[string]any{"term_id": termID, "data": data})
					}
				})
				termSvc.OnTerminalExit(func(termID string, exitCode int) {
					if app := application.Get(); app != nil && app.Event != nil {
						app.Event.Emit("terminal:exit", map[string]any{"term_id": termID, "exit_code": exitCode})
					}
				})
				if rt.workspaceFS != nil {
					rt.searchSvc = ideruntime.NewSearchService(rt.supervisor, rt.workspaceFS)
					rt.gitSvc = ideruntime.NewGitService(rt.supervisor, rt.workspaceFS)
				}
			}
			return c.Provide(svcIDE, true)
		}),
	}
}
