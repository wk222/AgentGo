package plugin

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type trace struct {
	mu sync.Mutex
	ev []string
}

func (t *trace) add(s string) { t.mu.Lock(); t.ev = append(t.ev, s); t.mu.Unlock() }
func (t *trace) get() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.ev...)
}
func (t *trace) reset() { t.mu.Lock(); t.ev = nil; t.mu.Unlock() }

func mustReg(t *testing.T, h *Host, p Plugin) {
	t.Helper()
	if err := h.Register(p); err != nil {
		t.Fatal(err)
	}
}

func state(t *testing.T, h *Host, name string) State {
	t.Helper()
	s, ok := h.Status(name)
	if !ok {
		t.Fatalf("no status for %s", name)
	}
	return s.State
}

// providerPlugin provides svc, logging apply/dispose.
func providerPlugin(tr *trace, name, svc string, inject ...string) Plugin {
	return Plugin{Name: name, Provides: []string{svc}, Inject: inject, Apply: func(c *Context) error {
		tr.add("start:" + name)
		c.Effect("log", func() { tr.add("stop:" + name) })
		return c.Provide(svc, name+"-instance")
	}}
}

func TestScopeReverseOrderIdempotentAndPanicSafe(t *testing.T) {
	s := NewScope("x")
	var got []int
	s.Effect("a", func() { got = append(got, 1) })
	s.Effect("boom", func() { panic("bad") })
	s.Effect("c", func() { got = append(got, 3) })
	err := s.Dispose()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected panic reported, got %v", err)
	}
	if !reflect.DeepEqual(got, []int{3, 1}) {
		t.Fatalf("order = %v", got)
	}
	if s.Dispose() != nil || len(got) != 2 {
		t.Fatal("second Dispose must be a no-op")
	}
	late := false
	s.Effect("late", func() { late = true })
	if !late {
		t.Fatal("effect registered after dispose must run immediately")
	}
}

func TestStartOrdersByDependenciesNotRegistration(t *testing.T) {
	tr := &trace{}
	h := NewHost(Hooks{})
	mustReg(t, h, providerPlugin(tr, "app", "app.svc", "mid.svc"))
	mustReg(t, h, providerPlugin(tr, "mid", "mid.svc", "base.svc"))
	mustReg(t, h, providerPlugin(tr, "base", "base.svc"))
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"start:base", "start:mid", "start:app"}) {
		t.Fatalf("order = %v", got)
	}
	tr.reset()
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"stop:app", "stop:mid", "stop:base"}) {
		t.Fatalf("shutdown order = %v", got)
	}
	if _, ok := h.Service("base.svc"); ok {
		t.Fatal("services must disappear on shutdown")
	}
}

func TestMissingRequiredIsSkippedOptionalIsFine(t *testing.T) {
	h := NewHost(Hooks{})
	var optErr error
	mustReg(t, h, Plugin{Name: "needs", Inject: []string{"nope"}, Apply: func(*Context) error { t.Fatal("must not run"); return nil }})
	mustReg(t, h, Plugin{Name: "opt", Optional: []string{"nope"}, Apply: func(c *Context) error {
		_, optErr = c.Service("nope")
		return nil
	}})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	st, _ := h.Status("needs")
	if st.State != StateSkipped || !reflect.DeepEqual(st.Missing, []string{"nope"}) {
		t.Fatalf("needs = %+v", st)
	}
	if state(t, h, "opt") != StateRunning || !errors.Is(optErr, ErrUnavailable) {
		t.Fatalf("opt state=%s err=%v", state(t, h, "opt"), optErr)
	}
}

func TestDependencyCycleIsRejected(t *testing.T) {
	h := NewHost(Hooks{})
	ran := false
	ap := func(*Context) error { ran = true; return nil }
	mustReg(t, h, Plugin{Name: "a", Provides: []string{"a"}, Inject: []string{"b"}, Apply: ap})
	mustReg(t, h, Plugin{Name: "b", Provides: []string{"b"}, Inject: []string{"a"}, Apply: ap})
	err := h.Start()
	if err == nil || !strings.Contains(err.Error(), "cycle") || ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

func TestFailureIsIsolatedAndCleansUp(t *testing.T) {
	tr := &trace{}
	h := NewHost(Hooks{})
	mustReg(t, h, Plugin{Name: "bad", Provides: []string{"bad.svc"}, Apply: func(c *Context) error {
		c.Effect("e", func() { tr.add("cleanup:bad") })
		if err := c.Provide("bad.svc", 1); err != nil {
			return err
		}
		return errors.New("kaboom")
	}})
	mustReg(t, h, providerPlugin(tr, "dep", "dep.svc", "bad.svc"))
	mustReg(t, h, providerPlugin(tr, "free", "free.svc"))
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if state(t, h, "bad") != StateFailed || state(t, h, "dep") != StateSkipped || state(t, h, "free") != StateRunning {
		t.Fatalf("states: %+v", h.Statuses())
	}
	if _, ok := h.Service("bad.svc"); ok {
		t.Fatal("failed plugin must not leave its service behind")
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"cleanup:bad", "start:free"}) {
		t.Fatalf("trace = %v", got)
	}
}

func TestPanicAndMissingProvideBecomeFailures(t *testing.T) {
	h := NewHost(Hooks{})
	mustReg(t, h, Plugin{Name: "panicky", Apply: func(*Context) error { panic("oops") }})
	mustReg(t, h, Plugin{Name: "liar", Provides: []string{"x"}, Apply: func(*Context) error { return nil }})
	_ = h.Start()
	if st, _ := h.Status("panicky"); st.State != StateFailed || !strings.Contains(st.Error, "oops") {
		t.Fatalf("panicky = %+v", st)
	}
	if st, _ := h.Status("liar"); st.State != StateFailed || !strings.Contains(st.Error, "was not provided") {
		t.Fatalf("liar = %+v", st)
	}
}

func TestUndeclaredUseIsRejected(t *testing.T) {
	h := NewHost(Hooks{})
	var provideErr, serviceErr error
	_, _ = h.Provide("core.thing", 42)
	mustReg(t, h, Plugin{Name: "p", Apply: func(c *Context) error {
		provideErr = c.Provide("sneaky", 1)
		_, serviceErr = c.Service("core.thing")
		return nil
	}})
	_ = h.Start()
	if provideErr == nil || serviceErr == nil {
		t.Fatalf("provide=%v service=%v", provideErr, serviceErr)
	}
}

func TestRegisterValidation(t *testing.T) {
	h := NewHost(Hooks{})
	_, _ = h.Provide("core.svc", 1)
	ap := func(*Context) error { return nil }
	if h.Register(Plugin{Name: "", Apply: ap}) == nil || h.Register(Plugin{Name: "x"}) == nil {
		t.Fatal("name and Apply required")
	}
	mustReg(t, h, Plugin{Name: "a", Provides: []string{"s"}, Apply: ap})
	if h.Register(Plugin{Name: "a", Apply: ap}) == nil {
		t.Fatal("duplicate name accepted")
	}
	if h.Register(Plugin{Name: "b", Provides: []string{"s"}, Apply: ap}) == nil {
		t.Fatal("two plugins providing the same service accepted")
	}
	if h.Register(Plugin{Name: "c", Provides: []string{"core.svc"}, Apply: ap}) == nil {
		t.Fatal("plugin shadowing a core service accepted")
	}
	if _, err := h.Provide("core.svc", 2); err == nil {
		t.Fatal("duplicate core service accepted")
	}
}

func TestStopCascadesAndReloadRestarts(t *testing.T) {
	tr := &trace{}
	h := NewHost(Hooks{})
	mustReg(t, h, providerPlugin(tr, "base", "base.svc"))
	mustReg(t, h, providerPlugin(tr, "mid", "mid.svc", "base.svc"))
	mustReg(t, h, providerPlugin(tr, "leaf", "leaf.svc", "mid.svc"))
	mustReg(t, h, providerPlugin(tr, "other", "other.svc", "base.svc"))
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}

	tr.reset()
	if err := h.Stop("other"); err != nil { // user disables "other"
		t.Fatal(err)
	}
	if st, _ := h.Status("other"); st.State != StateStopped || !st.Disabled {
		t.Fatalf("other = %+v", st)
	}

	tr.reset()
	if err := h.Stop("mid"); err != nil {
		t.Fatal(err)
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"stop:leaf", "stop:mid"}) {
		t.Fatalf("cascade order = %v", got)
	}
	if state(t, h, "base") != StateRunning {
		t.Fatal("provider below must keep running")
	}

	tr.reset()
	if err := h.Reload("base"); err != nil {
		t.Fatal(err)
	}
	// "mid" was disabled by the explicit Stop, so only base restarts.
	want := []string{"stop:base", "start:base"}
	if got := tr.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reload trace = %v want %v", got, want)
	}
	if state(t, h, "mid") != StateStopped || state(t, h, "other") != StateStopped {
		t.Fatalf("user-stopped plugins must stay stopped: %+v", h.Statuses())
	}

	tr.reset()
	if err := h.Reload("mid"); err != nil { // explicit reload re-enables mid and its dependents
		t.Fatal(err)
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"start:mid", "start:leaf"}) {
		t.Fatalf("reload mid = %v", got)
	}
	if v, _ := h.Service("base.svc"); v != "base-instance" {
		t.Fatalf("service after reload = %v", v)
	}
}

func TestReloadRetriesFailedPlugin(t *testing.T) {
	h := NewHost(Hooks{})
	attempts := 0
	mustReg(t, h, Plugin{Name: "flaky", Apply: func(*Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("first try fails")
		}
		return nil
	}})
	_ = h.Start()
	if state(t, h, "flaky") != StateFailed {
		t.Fatal("expected failure")
	}
	if err := h.Reload("flaky"); err != nil || state(t, h, "flaky") != StateRunning {
		t.Fatalf("reload err=%v state=%s", err, state(t, h, "flaky"))
	}
}

func TestEventMiddleware(t *testing.T) {
	tr := &trace{}
	h := NewHost(Hooks{})
	mustReg(t, h, Plugin{Name: "late", Apply: func(c *Context) error {
		c.OnPriority("tool", 10, func(p any, next func() error) error {
			tr.add("late:before")
			err := next()
			tr.add("late:after")
			return err
		})
		return nil
	}})
	mustReg(t, h, Plugin{Name: "early", Apply: func(c *Context) error {
		c.OnPriority("tool", -10, func(p any, next func() error) error {
			tr.add("early:" + p.(string))
			return next()
		})
		return nil
	}})
	mustReg(t, h, Plugin{Name: "gate", Apply: func(c *Context) error {
		c.On("blocked", func(any, func() error) error { return errors.New("denied") })
		return nil
	}})
	_ = h.Start()

	err := h.Emit("tool", "x", func() error { tr.add("final"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"early:x", "late:before", "final", "late:after"}
	if got := tr.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("chain = %v", got)
	}

	tr.reset()
	if err := h.Emit("blocked", nil, func() error { tr.add("final"); return nil }); err == nil || len(tr.get()) != 0 {
		t.Fatalf("short-circuit failed: err=%v trace=%v", err, tr.get())
	}

	if err := h.Emit("nobody-listens", nil, nil); err != nil {
		t.Fatalf("empty chain: %v", err)
	}

	_ = h.Stop("early")
	tr.reset()
	_ = h.Emit("tool", "y", nil)
	if got := tr.get(); !reflect.DeepEqual(got, []string{"late:before", "late:after"}) {
		t.Fatalf("handler of a stopped plugin still runs: %v", got)
	}
}

func TestEventHandlerMisbehaviour(t *testing.T) {
	h := NewHost(Hooks{})
	mustReg(t, h, Plugin{Name: "twice", Apply: func(c *Context) error {
		c.On("e1", func(_ any, next func() error) error { _ = next(); return next() })
		c.On("e2", func(any, func() error) error { panic("handler boom") })
		return nil
	}})
	_ = h.Start()
	if err := h.Emit("e1", nil, nil); !errors.Is(err, ErrNextTwice) {
		t.Fatalf("e1: %v", err)
	}
	if err := h.Emit("e2", nil, nil); err == nil || !strings.Contains(err.Error(), "handler boom") {
		t.Fatalf("e2: %v", err)
	}
}

func TestContributeHooksMirrorLifecycle(t *testing.T) {
	var mu sync.Mutex
	var log []string
	add := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	h := NewHost(Hooks{
		Contributed: func(p, k, n string) { add("+" + p + ":" + k + ":" + n) },
		Retired:     func(p, k, n string) { add("-" + p + ":" + k + ":" + n) },
	})
	mustReg(t, h, Plugin{Name: "crush", Apply: func(c *Context) error {
		c.Contribute("engine", "crush", func() { add("dispose-engine") })
		c.Contribute("workflow_node", "coding_agent", func() { panic("dispose fails") })
		return nil
	}})
	_ = h.Start()
	err := h.Stop("crush")
	if err == nil {
		t.Fatal("panicking dispose should be reported")
	}
	want := []string{
		"+crush:engine:crush", "+crush:workflow_node:coding_agent",
		"-crush:workflow_node:coding_agent", // retired even though dispose panicked
		"dispose-engine", "-crush:engine:crush",
	}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("hooks = %v\nwant    %v", log, want)
	}
}

func TestResolveTyped(t *testing.T) {
	type router interface{ ID() string }
	h := NewHost(Hooks{})
	if _, err := Resolve[router](h, "engines"); err == nil {
		t.Fatal("missing service must error")
	}
	_, _ = h.Provide("engines", "not a router")
	if _, err := Resolve[router](h, "engines"); err == nil {
		t.Fatal("wrong type must error")
	}
	_, _ = h.Provide("n", 7)
	if n, err := Resolve[int](h, "n"); err != nil || n != 7 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestUseTypedInsidePlugin(t *testing.T) {
	h := NewHost(Hooks{})
	_, _ = h.Provide("greeter", func() string { return "hi" })
	var got string
	mustReg(t, h, Plugin{Name: "p", Inject: []string{"greeter"}, Apply: func(c *Context) error {
		g, err := Use[func() string](c, "greeter")
		if err != nil {
			return err
		}
		got = g()
		return nil
	}})
	_ = h.Start()
	if got != "hi" || state(t, h, "p") != StateRunning {
		t.Fatalf("got=%q state=%s", got, state(t, h, "p"))
	}
}

func TestConcurrentObserveWhileReloading(t *testing.T) {
	h := NewHost(Hooks{})
	tr := &trace{}
	mustReg(t, h, providerPlugin(tr, "base", "base.svc"))
	mustReg(t, h, Plugin{Name: "mw", Inject: []string{"base.svc"}, Apply: func(c *Context) error {
		c.On("ping", func(_ any, next func() error) error { return next() })
		return nil
	}})
	_ = h.Start()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = h.Statuses()
					_ = h.Emit("ping", nil, nil)
					_, _ = h.Service("base.svc")
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if err := h.Reload("base"); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
	if state(t, h, "mw") != StateRunning {
		t.Fatalf("mw = %s", state(t, h, "mw"))
	}
}

func TestLateRegisterAfterStart(t *testing.T) {
	h := NewHost(Hooks{})
	tr := &trace{}
	mustReg(t, h, providerPlugin(tr, "base", "base.svc"))
	_ = h.Start()
	mustReg(t, h, providerPlugin(tr, "late", "late.svc", "base.svc"))
	_ = h.Start()
	if state(t, h, "late") != StateRunning {
		t.Fatalf("late plugin not started: %+v", h.Statuses())
	}
	if got := tr.get(); !reflect.DeepEqual(got, []string{"start:base", "start:late"}) {
		t.Fatalf("trace = %v (base must not be restarted)", got)
	}
}
