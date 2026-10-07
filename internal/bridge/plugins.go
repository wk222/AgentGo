package bridge

import (
	"context"
	"log"

	"agentgo/internal/capability"
	"agentgo/internal/engine"
	"agentgo/internal/plugin"
)

// Core services every plugin may Inject.
const (
	svcEngines    = "engines"    // *engine.Router
	svcCapability = "capability" // *capability.Bus
	svcTools      = "tools"      // *tools.Registry
	svcBrain      = "brain"      // *AppService: the chat path and session API that engines and protocol adapters drive
	svcCrush      = "crush-engine" // *crushengine.Engine, while the crush plugin runs
)

// chatBackend is all the Eino engine needs from the application: run one chat
// turn. Naming it keeps the engine from reaching into the rest of AppService.
type chatBackend interface {
	sendMessageCore(ctx context.Context, sessionID, userText string, images []string, streamEmit func(string)) SendMessageResult
}

func pluginScope(p string) string { return "plugin:" + p }

func grantID(plug, kind, name string) string { return kind + ":" + name + ":" + pluginScope(plug) }

// governanceHooks mirrors plugin contributions into the capability bus, so each
// contribution gets a grant (audit, risk, lifecycle) without the plugin doing
// anything. Unloading retires the grant; loading again revives it.
func governanceHooks(bus *capability.Bus) plugin.Hooks {
	if bus == nil {
		return plugin.Hooks{}
	}
	return governanceHooksFor(func() *capability.Bus { return bus })
}

// governanceHooksFor looks the bus up when a contribution happens, because the
// host exists before the capability plugin has created the bus. Until then
// contributions are not mirrored.
func governanceHooksFor(bus func() *capability.Bus) plugin.Hooks {
	return plugin.Hooks{
		Contributed: func(plug, kind, name string) {
			b := bus()
			if b == nil {
				return
			}
			g := b.Register(kind, name, pluginScope(plug), map[string]string{"plugin": plug})
			if g.Status == capability.StatusRetired {
				_ = b.Transition(g.ID, capability.StatusDraft)
				_ = b.Transition(g.ID, capability.StatusPublished)
			}
		},
		Retired: func(plug, kind, name string) {
			if b := bus(); b != nil {
				_ = b.Transition(grantID(plug, kind, name), capability.StatusRetired)
			}
		},
	}
}

// initPlugins builds the host, publishes core services, registers the
// first-party plugins and starts them. A plugin that fails or is skipped is
// logged and visible through ListPlugins; it never blocks the app.
func (s *AppService) initPlugins() {
	var bus *capability.Bus
	if s.rt != nil {
		bus = s.rt.capBus
	}
	// A runtime built by NewRuntime already has a host whose plugins provide the
	// capability bus, the tool registry and the rest; the first-party plugins
	// join it. A hand-assembled runtime (tests) gets a host of its own.
	shared := s.rt != nil && s.rt.host != nil
	var h *plugin.Host
	if shared {
		h = s.rt.host
	} else {
		h = plugin.NewHost(governanceHooks(bus))
	}
	s.plugins = h

	_, _ = h.Provide(svcEngines, s.engines)
	_, _ = h.Provide(svcBrain, s)
	if s.rt != nil {
		s.rt.engineRouter = s.engines
	}
	if !shared {
		if bus != nil {
			_, _ = h.Provide(svcCapability, bus)
		}
		if s.rt != nil && s.rt.toolReg != nil {
			_, _ = h.Provide(svcTools, s.rt.toolReg)
		}
	}

	register := func(p plugin.Plugin, ok bool) {
		if !ok {
			return
		}
		if err := h.Register(p); err != nil {
			log.Printf("[plugin] register %s: %v", p.Name, err)
		}
	}
	register(einoPlugin(), true)
	register(crushPlugin(s.rt))
	ctPlugin, ctEnabled := codetoolsPlugin(s.codetoolsEnv())
	register(ctPlugin, ctEnabled)
	if ctEnabled {
		s.unbindWorkspace = s.rt.AddWorkspaceBinder(s.rebindCodetools)
	}
	register(crushprotoPlugin(s.rt))
	register(hostlinkPlugin(s.rt))

	if err := h.Start(); err != nil {
		log.Printf("[plugin] start: %v", err)
	}
	for _, st := range h.Statuses() {
		if st.State == plugin.StateRunning {
			log.Printf("[plugin] %s running", st.Name)
		} else {
			log.Printf("[plugin] %s %s: %s", st.Name, st.State, st.Error)
		}
	}
}

// einoPlugin contributes the built-in Eino engine (the default engine).
func einoPlugin() plugin.Plugin {
	return plugin.Plugin{
		Name:   "eino",
		Inject: []string{svcEngines, svcBrain},
		Apply: func(c *plugin.Context) error {
			router, err := plugin.Use[*engine.Router](c, svcEngines)
			if err != nil {
				return err
			}
			backend, err := plugin.Use[chatBackend](c, svcBrain)
			if err != nil {
				return err
			}
			c.Contribute("engine", EinoEngineID, router.Register(NewEinoEngine(backend)))
			return nil
		},
	}
}
