package plugin

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// State is a plugin's lifecycle state.
type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateFailed  State = "failed"  // Apply returned an error / panicked
	StateSkipped State = "skipped" // a required service is unavailable
	StateStopped State = "stopped"
)

// CoreOwner owns services provided directly through Host.Provide.
const CoreOwner = "core"

// Plugin is the unit of extension. Provides/Inject/Optional are a contract: the
// host orders startup by them, and Context rejects anything undeclared.
type Plugin struct {
	Name     string
	Provides []string // services Apply must provide
	Inject   []string // required services; missing => Skipped
	Optional []string // optional services; still affect ordering and cascades
	Apply    func(*Context) error
	// Protected marks a plugin the runtime cannot work without (storage,
	// sessions, the agent...). Users cannot stop, reload or disable it, and
	// nothing they stop may cascade onto it; only Shutdown tears it down.
	Protected bool
}

// ErrProtected is returned when a protected plugin is stopped, reloaded or
// disabled, directly or through a dependency.
var ErrProtected = errors.New("plugin: protected plugin")

func (p Plugin) deps() []string { return append(append([]string(nil), p.Inject...), p.Optional...) }

// Status is an observable snapshot of one plugin.
type Status struct {
	Name     string   `json:"name"`
	State    State    `json:"state"`
	Error    string   `json:"error,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Provides []string `json:"provides,omitempty"`
	Inject   []string `json:"inject,omitempty"`
	Optional []string `json:"optional,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
	// Protected plugins are part of the runtime core and cannot be stopped.
	Protected bool `json:"protected,omitempty"`
}

// Hooks let the embedding application observe contributions (e.g. to mirror
// them into the capability bus for governance/audit).
type Hooks struct {
	Contributed func(plugin, kind, name string)
	Retired     func(plugin, kind, name string)
}

type service struct {
	owner string
	v     any
}

type record struct {
	p        Plugin
	state    State
	err      error
	missing  []string
	scope    *Scope
	disabled bool // explicitly stopped by the user; Reload of a provider will not revive it
}

// Host owns services, plugins and the event bus.
type Host struct {
	life     sync.Mutex   // serializes Start/Stop/Reload/Shutdown
	mu       sync.RWMutex // guards the maps and record fields
	services map[string]service
	plugins  map[string]*record
	order    []string
	events   *eventBus
	hooks    Hooks
}

func NewHost(hooks Hooks) *Host {
	return &Host{
		services: map[string]service{},
		plugins:  map[string]*record{},
		events:   newEventBus(),
		hooks:    hooks,
	}
}

// Provide registers a core service (not owned by any plugin).
func (h *Host) Provide(name string, v any) (dispose func(), err error) {
	return h.provide(CoreOwner, name, v)
}

func (h *Host) provide(owner, name string, v any) (func(), error) {
	if strings.TrimSpace(name) == "" || v == nil {
		return nil, errors.New("plugin: service name and value are required")
	}
	h.mu.Lock()
	if ex, ok := h.services[name]; ok {
		h.mu.Unlock()
		return nil, fmt.Errorf("plugin: service %q already provided by %s", name, ex.owner)
	}
	h.services[name] = service{owner: owner, v: v}
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if s, ok := h.services[name]; ok && s.owner == owner {
			delete(h.services, name)
		}
		h.mu.Unlock()
	}, nil
}

// Service returns a provided service.
func (h *Host) Service(name string) (any, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.services[name]
	return s.v, ok
}

// Resolve is the typed form of Service for code outside plugins.
func Resolve[T any](h *Host, name string) (T, error) {
	var zero T
	v, ok := h.Service(name)
	if !ok {
		return zero, fmt.Errorf("plugin: service %q is not available", name)
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("plugin: service %q has type %T", name, v)
	}
	return t, nil
}

// Emit runs the middleware chain for event, then final.
func (h *Host) Emit(event string, payload any, final func() error) error {
	return h.events.emit(event, payload, final)
}

// On subscribes from outside any plugin (the returned func unsubscribes).
func (h *Host) On(event string, fn Handler) (dispose func()) { return h.events.on(event, 0, fn) }

// Register declares a plugin; it does not run until Start.
func (h *Host) Register(p Plugin) error {
	if strings.TrimSpace(p.Name) == "" || p.Apply == nil {
		return errors.New("plugin: name and Apply are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.plugins[p.Name]; ok {
		return fmt.Errorf("plugin: %q already registered", p.Name)
	}
	for _, s := range p.Provides {
		if ex, ok := h.services[s]; ok && ex.owner == CoreOwner {
			return fmt.Errorf("plugin: %q cannot provide %q: owned by core", p.Name, s)
		}
		for _, other := range h.plugins {
			for _, os := range other.p.Provides {
				if os == s {
					return fmt.Errorf("plugin: %q cannot provide %q: already declared by %q", p.Name, s, other.p.Name)
				}
			}
		}
	}
	h.plugins[p.Name] = &record{p: p, state: StatePending}
	h.order = append(h.order, p.Name)
	return nil
}

// Start runs every pending plugin in dependency order. Plugin failures are
// recorded in Statuses and do not abort startup; only a dependency cycle does.
func (h *Host) Start() error {
	h.life.Lock()
	defer h.life.Unlock()
	var names []string
	h.mu.RLock()
	for _, n := range h.order {
		if r := h.plugins[n]; r.state == StatePending && !r.disabled {
			names = append(names, n)
		}
	}
	h.mu.RUnlock()
	order, err := h.plan(names)
	if err != nil {
		return err
	}
	for _, n := range order {
		h.startOne(h.rec(n))
	}
	return nil
}

// Stop stops a plugin and, first, everything that depends on it.
func (h *Host) Stop(name string) error {
	h.life.Lock()
	defer h.life.Unlock()
	r := h.rec(name)
	if r == nil {
		return fmt.Errorf("plugin: unknown plugin %q", name)
	}
	set := append([]string{name}, h.dependents(name)...)
	if err := h.refuseProtected(set); err != nil {
		return err
	}
	order, err := h.plan(h.inRegistrationOrder(set))
	if err != nil {
		return err
	}
	errs := h.stopOrdered(order)
	h.mu.Lock()
	r.disabled = true
	h.mu.Unlock()
	return errors.Join(errs...)
}

// Reload restarts a plugin and everything depending on it (dependents that the
// user stopped explicitly stay stopped). Also retries failed/skipped plugins.
func (h *Host) Reload(name string) error {
	h.life.Lock()
	defer h.life.Unlock()
	self := h.rec(name)
	if self == nil {
		return fmt.Errorf("plugin: unknown plugin %q", name)
	}
	set := append([]string{name}, h.dependents(name)...)
	if err := h.refuseProtected(set); err != nil {
		return err
	}
	order, err := h.plan(h.inRegistrationOrder(set))
	if err != nil {
		return err
	}
	errs := h.stopOrdered(order)
	h.mu.Lock()
	self.disabled = false
	h.mu.Unlock()
	for _, n := range order {
		r := h.rec(n)
		h.mu.RLock()
		skip := r.disabled
		h.mu.RUnlock()
		if !skip {
			h.startOne(r)
		}
	}
	return errors.Join(errs...)
}

// Shutdown stops every running plugin, dependents first.
func (h *Host) Shutdown() error {
	h.life.Lock()
	defer h.life.Unlock()
	h.mu.RLock()
	all := append([]string(nil), h.order...)
	h.mu.RUnlock()
	order, err := h.plan(all)
	if err != nil {
		order = all // still tear everything down in reverse registration order
	}
	return errors.Join(h.stopOrdered(order)...)
}

// Statuses lists plugins in registration order.
func (h *Host) Statuses() []Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Status, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, statusOf(h.plugins[n]))
	}
	return out
}

// Status returns one plugin's snapshot.
func (h *Host) Status(name string) (Status, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.plugins[name]
	if !ok {
		return Status{}, false
	}
	return statusOf(r), true
}

// refuseProtected fails if any of names is protected. The caller holds h.life.
func (h *Host) refuseProtected(names []string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, n := range names {
		if r := h.plugins[n]; r != nil && r.p.Protected {
			return fmt.Errorf("%w: %q cannot be stopped or reloaded", ErrProtected, n)
		}
	}
	return nil
}

// Disable keeps a registered plugin from starting (configuration decides the
// enabled set before Start). Plugins that need its services are skipped with
// the missing service named. A plugin that is already running is stopped with
// Stop instead, and a protected one cannot be disabled.
func (h *Host) Disable(name string) error {
	h.life.Lock()
	defer h.life.Unlock()
	r := h.rec(name)
	if r == nil {
		return fmt.Errorf("plugin: unknown plugin %q", name)
	}
	if r.p.Protected {
		return fmt.Errorf("%w: %q cannot be disabled", ErrProtected, name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.state == StateRunning {
		return fmt.Errorf("plugin: %q is running; use Stop", name)
	}
	r.disabled = true
	return nil
}

// Edge is one dependency between plugins through a service.
type Edge struct {
	From     string `json:"from"`     // the plugin that needs the service
	To       string `json:"to"`       // the plugin that provides it ("core" for Host.Provide)
	Service  string `json:"service"`
	Optional bool   `json:"optional,omitempty"`
	// Resolved is true when the service is available right now.
	Resolved bool `json:"resolved"`
}

// Graph is the actual dependency graph: every plugin with its state, and every
// declared need with who provides it and whether it is available.
type Graph struct {
	Nodes []Status `json:"nodes"`
	Edges []Edge   `json:"edges"`
}

// Graph returns the dependency graph as it is now. A need nobody provides gets
// an edge to "" so a missing provider is visible, not silently absent.
func (h *Host) Graph() Graph {
	h.mu.RLock()
	defer h.mu.RUnlock()
	owner := map[string]string{}
	for name, s := range h.services {
		owner[name] = s.owner
	}
	for _, n := range h.order {
		for _, s := range h.plugins[n].p.Provides {
			if _, ok := owner[s]; !ok {
				owner[s] = n // declared, not (or no longer) provided
			}
		}
	}
	g := Graph{Nodes: make([]Status, 0, len(h.order)), Edges: []Edge{}}
	for _, n := range h.order {
		r := h.plugins[n]
		g.Nodes = append(g.Nodes, statusOf(r))
		add := func(svcs []string, optional bool) {
			for _, s := range svcs {
				_, up := h.services[s]
				g.Edges = append(g.Edges, Edge{From: n, To: owner[s], Service: s, Optional: optional, Resolved: up})
			}
		}
		add(r.p.Inject, false)
		add(r.p.Optional, true)
	}
	return g
}

func statusOf(r *record) Status {
	s := Status{
		Name: r.p.Name, State: r.state, Missing: append([]string(nil), r.missing...),
		Provides: r.p.Provides, Inject: r.p.Inject, Optional: r.p.Optional, Disabled: r.disabled,
		Protected: r.p.Protected,
	}
	if r.err != nil {
		s.Error = r.err.Error()
	}
	return s
}
