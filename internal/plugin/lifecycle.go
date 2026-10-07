package plugin

import (
	"fmt"
	"strings"
)

func (h *Host) rec(name string) *record {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.plugins[name]
}

func (h *Host) hasService(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.services[name]
	return ok
}

func (h *Host) ownedBy(service, owner string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.services[service]
	return ok && s.owner == owner
}

func (h *Host) setState(r *record, st State, err error, missing []string, sc *Scope) {
	h.mu.Lock()
	r.state, r.err, r.missing, r.scope = st, err, missing, sc
	h.mu.Unlock()
}

// startOne runs a plugin's Apply. Caller holds h.life.
func (h *Host) startOne(r *record) {
	var missing []string
	for _, s := range r.p.Inject {
		if !h.hasService(s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		h.setState(r, StateSkipped, fmt.Errorf("missing services: %s", strings.Join(missing, ", ")), missing, nil)
		return
	}
	sc := NewScope(r.p.Name)
	err := safeApply(r.p.Apply, &Context{h: h, r: r, scope: sc})
	if err == nil {
		for _, s := range r.p.Provides {
			if !h.ownedBy(s, r.p.Name) {
				err = fmt.Errorf("declared service %q was not provided by Apply", s)
				break
			}
		}
	}
	if err != nil {
		_ = sc.Dispose()
		h.setState(r, StateFailed, err, nil, nil)
		return
	}
	h.setState(r, StateRunning, nil, nil, sc)
}

func safeApply(fn func(*Context) error, c *Context) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("plugin %q panicked in Apply: %v", c.r.p.Name, rec)
		}
	}()
	return fn(c)
}

// stopOrdered stops running plugins in reverse of order (dependents first).
func (h *Host) stopOrdered(order []string) []error {
	var errs []error
	for i := len(order) - 1; i >= 0; i-- {
		r := h.rec(order[i])
		h.mu.RLock()
		sc := r.scope
		h.mu.RUnlock()
		if sc == nil {
			continue
		}
		h.setState(r, StateStopped, nil, nil, nil)
		if err := sc.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// plan topologically orders names (given in registration order) so providers
// start before consumers; ties keep registration order.
func (h *Host) plan(names []string) ([]string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	provider := map[string]string{}
	for _, n := range names {
		for _, s := range h.plugins[n].p.Provides {
			provider[s] = n
		}
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	seen := map[[2]string]bool{}
	for _, n := range names {
		indeg[n] += 0
		for _, d := range h.plugins[n].p.deps() {
			q, ok := provider[d]
			if !ok || q == n || seen[[2]string{q, n}] {
				continue
			}
			seen[[2]string{q, n}] = true
			adj[q] = append(adj[q], n)
			indeg[n]++
		}
	}
	placed := map[string]bool{}
	out := make([]string, 0, len(names))
	for len(out) < len(names) {
		progressed := false
		for _, n := range names {
			if placed[n] || indeg[n] > 0 {
				continue
			}
			placed[n] = true
			out = append(out, n)
			for _, m := range adj[n] {
				indeg[m]--
			}
			progressed = true
			break
		}
		if !progressed {
			var rest []string
			for _, n := range names {
				if !placed[n] {
					rest = append(rest, n)
				}
			}
			return nil, fmt.Errorf("plugin: dependency cycle among: %s", strings.Join(rest, ", "))
		}
	}
	return out, nil
}

// dependents returns plugins that (transitively) depend on root's services.
func (h *Host) dependents(root string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	provided := map[string]bool{}
	for _, s := range h.plugins[root].p.Provides {
		provided[s] = true
	}
	found := map[string]bool{root: true}
	for changed := true; changed; {
		changed = false
		for _, n := range h.order {
			if found[n] {
				continue
			}
			for _, d := range h.plugins[n].p.deps() {
				if provided[d] {
					found[n] = true
					changed = true
					for _, s := range h.plugins[n].p.Provides {
						provided[s] = true
					}
					break
				}
			}
		}
	}
	var out []string
	for _, n := range h.order {
		if found[n] && n != root {
			out = append(out, n)
		}
	}
	return out
}

func (h *Host) inRegistrationOrder(names []string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []string
	for _, n := range h.order {
		if want[n] {
			out = append(out, n)
		}
	}
	return out
}
