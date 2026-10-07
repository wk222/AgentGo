package plugin

import (
	"errors"
	"fmt"
	"slices"
)

// ErrUnavailable is returned by Service for a declared but absent (optional) service.
var ErrUnavailable = errors.New("plugin: service unavailable")

// Context is what a plugin sees. Everything registered through it is tied to
// the plugin's Scope and is released when the plugin stops or reloads.
type Context struct {
	h     *Host
	r     *record
	scope *Scope
}

func (c *Context) Name() string  { return c.r.p.Name }
func (c *Context) Scope() *Scope { return c.scope }

// Effect registers a cleanup run when the plugin stops.
func (c *Context) Effect(label string, fn func()) { c.scope.Effect(label, fn) }

// Contribute records that this plugin added something (kind e.g. "engine",
// "tool", "workflow_node", "panel") and arranges for dispose to run on stop.
// Hooks are notified so governance can mirror the contribution automatically.
func (c *Context) Contribute(kind, name string, dispose func()) {
	plug, hooks := c.r.p.Name, c.h.hooks
	if hooks.Contributed != nil {
		hooks.Contributed(plug, kind, name)
	}
	c.scope.Effect(kind+":"+name, func() {
		if hooks.Retired != nil {
			defer hooks.Retired(plug, kind, name)
		}
		if dispose != nil {
			dispose()
		}
	})
}

// Provide publishes a service; it must be declared in Plugin.Provides.
func (c *Context) Provide(name string, v any) error {
	if !slices.Contains(c.r.p.Provides, name) {
		return fmt.Errorf("plugin %q: service %q is not declared in Provides", c.r.p.Name, name)
	}
	dispose, err := c.h.provide(c.r.p.Name, name, v)
	if err != nil {
		return err
	}
	c.scope.Effect("service:"+name, dispose)
	return nil
}

// Service fetches a declared dependency.
func (c *Context) Service(name string) (any, error) {
	if !slices.Contains(c.r.p.Inject, name) && !slices.Contains(c.r.p.Optional, name) {
		return nil, fmt.Errorf("plugin %q: service %q is not declared in Inject/Optional", c.r.p.Name, name)
	}
	v, ok := c.h.Service(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, name)
	}
	return v, nil
}

// Use is the typed form of Context.Service.
func Use[T any](c *Context, name string) (T, error) {
	var zero T
	v, err := c.Service(name)
	if err != nil {
		return zero, err
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("plugin %q: service %q has type %T", c.r.p.Name, name, v)
	}
	return t, nil
}

// On subscribes to an event with middleware semantics.
func (c *Context) On(event string, fn Handler) { c.OnPriority(event, 0, fn) }

// OnPriority orders handlers; lower runs first.
func (c *Context) OnPriority(event string, prio int, fn Handler) {
	c.scope.Effect("on:"+event, c.h.events.on(event, prio, fn))
}

// Emit runs the event chain, then final.
func (c *Context) Emit(event string, payload any, final func() error) error {
	return c.h.events.emit(event, payload, final)
}
