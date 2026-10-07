package plugin

import (
	"errors"
	"testing"
)

// svcPlugin builds a plugin that provides `provides` and records start/stop in log.
func svcPlugin(name string, provides, inject, optional []string, protected bool, log *[]string) Plugin {
	return Plugin{
		Name: name, Provides: provides, Inject: inject, Optional: optional, Protected: protected,
		Apply: func(c *Context) error {
			*log = append(*log, "start:"+name)
			for _, s := range provides {
				if err := c.Provide(s, name); err != nil {
					return err
				}
			}
			c.Effect("stop", func() { *log = append(*log, "stop:"+name) })
			return nil
		},
	}
}

func TestProtectedPluginsCannotBeStoppedReloadedOrDisabled(t *testing.T) {
	var log []string
	h := NewHost(Hooks{})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.Register(svcPlugin("store", []string{"db"}, nil, nil, true, &log)))
	must(h.Register(svcPlugin("sessions", []string{"sess"}, []string{"db"}, nil, true, &log)))
	must(h.Register(svcPlugin("extra", []string{"x"}, []string{"sess"}, nil, false, &log)))
	must(h.Start())

	for name, op := range map[string]func(string) error{"Stop": h.Stop, "Reload": h.Reload, "Disable": h.Disable} {
		if err := op("store"); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s(store) = %v, want ErrProtected", name, err)
		}
		if err := op("sessions"); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s(sessions) = %v, want ErrProtected", name, err)
		}
	}
	// Refusing must not have stopped anything on the way.
	for _, st := range h.Statuses() {
		if st.State != StateRunning {
			t.Fatalf("%s is %s after refused operations", st.Name, st.State)
		}
	}
	if st, _ := h.Status("store"); !st.Protected {
		t.Fatal("status must show protected")
	}
	// An unprotected leaf is stoppable and reloadable as before.
	must(h.Stop("extra"))
	must(h.Reload("extra"))
}

func TestProtectedDependentBlocksStoppingItsProvider(t *testing.T) {
	var log []string
	h := NewHost(Hooks{})
	_ = h.Register(svcPlugin("optional-provider", []string{"p"}, nil, nil, false, &log))
	_ = h.Register(svcPlugin("core-user", []string{"c"}, []string{"p"}, nil, true, &log))
	_ = h.Start()

	if err := h.Stop("optional-provider"); !errors.Is(err, ErrProtected) {
		t.Fatalf("stopping a provider of a protected plugin = %v, want ErrProtected", err)
	}
	if st, _ := h.Status("core-user"); st.State != StateRunning {
		t.Fatalf("protected dependent was stopped: %s", st.State)
	}
	if st, _ := h.Status("optional-provider"); st.State != StateRunning {
		t.Fatalf("provider was stopped despite the refusal: %s", st.State)
	}
}

func TestShutdownTearsDownProtectedPluginsDependentsFirst(t *testing.T) {
	var log []string
	h := NewHost(Hooks{})
	_ = h.Register(svcPlugin("store", []string{"db"}, nil, nil, true, &log))
	_ = h.Register(svcPlugin("agent", []string{"agent"}, []string{"db"}, nil, true, &log))
	_ = h.Register(svcPlugin("gateway", nil, []string{"agent"}, nil, false, &log))
	_ = h.Start()
	log = nil

	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop:gateway", "stop:agent", "stop:store"}
	if len(log) != 3 || log[0] != want[0] || log[1] != want[1] || log[2] != want[2] {
		t.Fatalf("shutdown order = %v, want %v", log, want)
	}
	if err := h.Shutdown(); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
	if len(log) != 3 {
		t.Fatalf("second shutdown stopped things again: %v", log)
	}
}

func TestDisableKeepsAPluginFromStartingAndSkipsItsConsumers(t *testing.T) {
	var log []string
	h := NewHost(Hooks{})
	_ = h.Register(svcPlugin("store", []string{"db"}, nil, nil, true, &log))
	_ = h.Register(svcPlugin("ide", []string{"ide"}, []string{"db"}, nil, false, &log))
	_ = h.Register(svcPlugin("needs-ide", nil, []string{"ide"}, nil, false, &log))
	_ = h.Register(svcPlugin("likes-ide", nil, nil, []string{"ide"}, false, &log))
	if err := h.Disable("ide"); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable("nope"); err == nil {
		t.Fatal("unknown plugin must be an error")
	}
	_ = h.Start()

	ide, _ := h.Status("ide")
	if ide.State != StatePending || !ide.Disabled {
		t.Fatalf("ide = %+v", ide)
	}
	needs, _ := h.Status("needs-ide")
	if needs.State != StateSkipped || len(needs.Missing) != 1 || needs.Missing[0] != "ide" {
		t.Fatalf("a hard consumer must be skipped with the missing service named: %+v", needs)
	}
	if likes, _ := h.Status("likes-ide"); likes.State != StateRunning {
		t.Fatalf("an optional consumer must still start: %+v", likes)
	}

	// Enabling later is a Reload; its consumers come back with it.
	if err := h.Reload("ide"); err != nil {
		t.Fatal(err)
	}
	if st, _ := h.Status("ide"); st.State != StateRunning || st.Disabled {
		t.Fatalf("ide after reload = %+v", st)
	}
	if err := h.Disable("ide"); err == nil {
		t.Fatal("a running plugin must be stopped, not disabled")
	}
}

func TestGraphShowsRealDependenciesAndWhatIsMissing(t *testing.T) {
	var log []string
	h := NewHost(Hooks{})
	_, _ = h.Provide("engines", "router")
	_ = h.Register(svcPlugin("store", []string{"db"}, nil, nil, true, &log))
	_ = h.Register(svcPlugin("memory", []string{"mem"}, []string{"db"}, nil, false, &log))
	_ = h.Register(svcPlugin("agent", []string{"agent"}, []string{"db", "engines"}, []string{"mem", "voice"}, false, &log))
	_ = h.Start()

	g := h.Graph()
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d", len(g.Nodes))
	}
	find := func(from, svc string) Edge {
		t.Helper()
		for _, e := range g.Edges {
			if e.From == from && e.Service == svc {
				return e
			}
		}
		t.Fatalf("no edge %s -> %s in %+v", from, svc, g.Edges)
		return Edge{}
	}
	if e := find("memory", "db"); e.To != "store" || !e.Resolved || e.Optional {
		t.Fatalf("memory->db = %+v", e)
	}
	if e := find("agent", "engines"); e.To != CoreOwner || !e.Resolved {
		t.Fatalf("core service must name core as provider: %+v", e)
	}
	if e := find("agent", "mem"); e.To != "memory" || !e.Optional || !e.Resolved {
		t.Fatalf("agent->mem = %+v", e)
	}
	if e := find("agent", "voice"); e.To != "" || e.Resolved {
		t.Fatalf("a need nobody provides must show as unresolved with no provider: %+v", e)
	}

	// Stopping a provider makes the edge unresolved but still attributed to it.
	if err := h.Stop("memory"); err != nil {
		t.Fatal(err)
	}
	g = h.Graph()
	seen := false
	for _, e := range g.Edges {
		if e.From == "agent" && e.Service == "mem" {
			seen = true
			if e.Resolved || e.To != "memory" {
				t.Fatalf("stopped provider edge = %+v", e)
			}
		}
	}
	if !seen {
		t.Fatal("edge disappeared")
	}
}
