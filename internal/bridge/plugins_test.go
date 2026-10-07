package bridge

import (
	"os"
	"reflect"
	"testing"

	"agentgo/internal/capability"
	"agentgo/internal/plugin"
	"agentgo/internal/workflow"
)

func pluginState(t *testing.T, s *AppService, name string) plugin.State {
	t.Helper()
	for _, st := range s.ListPlugins() {
		if st.Name == name {
			return st.State
		}
	}
	t.Fatalf("plugin %q not listed: %+v", name, s.ListPlugins())
	return ""
}

func grantStatus(bus *capability.Bus, kind, plug, name string) (capability.AssetStatus, bool) {
	for _, g := range bus.List(kind) {
		if g.ID == grantID(plug, kind, name) {
			return g.Status, true
		}
	}
	return "", false
}

func TestEinoIsAPluginAndStaysDefault(t *testing.T) {
	t.Setenv(crushEnvExe, "") // empty => fall back to discovery next to the test binary: finds nothing
	s := NewAppService(nil)
	defer s.Close()
	if pluginState(t, s, "eino") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	if got := s.ListEngines(); !reflect.DeepEqual(got, []string{EinoEngineID}) {
		t.Fatalf("engines = %v", got)
	}
	if r := s.StopPlugin("eino"); r["success"] != true {
		t.Fatalf("stop: %v", r)
	}
	if len(s.ListEngines()) != 0 {
		t.Fatalf("engine must be withdrawn with its plugin: %v", s.ListEngines())
	}
	if r := s.ReloadPlugin("eino"); r["success"] != true {
		t.Fatalf("reload: %v", r)
	}
	if got := s.ListEngines(); !reflect.DeepEqual(got, []string{EinoEngineID}) {
		t.Fatalf("engines after reload = %v", got)
	}
	if r := s.ReloadPlugin("nope"); r["success"] != false {
		t.Fatalf("unknown plugin must fail: %v", r)
	}
}

func TestCrushPluginLifecycleAndGovernance(t *testing.T) {
	self, err := os.Executable() // any existing file satisfies discovery; the sidecar is never started
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(crushEnvExe, self)
	bus := capability.NewBus()
	s := NewAppService(&Runtime{capBus: bus})
	defer s.Close()

	if pluginState(t, s, "crush") != plugin.StateRunning || s.crushEngine() == nil {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	if got := s.ListEngines(); !reflect.DeepEqual(got, []string{"crush", EinoEngineID}) {
		t.Fatalf("engines = %v", got)
	}
	if !workflow.NodeTypeAvailable("coding_agent") || !workflow.NodeTypeAvailable("crush") {
		t.Fatal("coding_agent nodes must be contributed by the crush plugin")
	}
	for _, c := range []struct{ kind, plug, name string }{
		{"engine", "crush", "crush"}, {"engine", "eino", EinoEngineID}, {"workflow_node", "crush", "coding_agent"},
	} {
		if st, ok := grantStatus(bus, c.kind, c.plug, c.name); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %+v: %v %v", c, st, ok)
		}
	}

	// Stopping withdraws the engine and the node, and retires the grants.
	if r := s.StopPlugin("crush"); r["success"] != true {
		t.Fatalf("stop: %v", r)
	}
	if got := s.ListEngines(); !reflect.DeepEqual(got, []string{EinoEngineID}) {
		t.Fatalf("engines after stop = %v", got)
	}
	if workflow.NodeTypeAvailable("coding_agent") {
		t.Fatal("node type must disappear with the plugin")
	}
	if st, _ := grantStatus(bus, "engine", "crush", "crush"); st != capability.StatusRetired {
		t.Fatalf("grant status after stop = %q", st)
	}

	// Reloading brings everything back and revives the grants.
	if r := s.ReloadPlugin("crush"); r["success"] != true {
		t.Fatalf("reload: %v", r)
	}
	if !workflow.NodeTypeAvailable("coding_agent") || len(s.ListEngines()) != 2 {
		t.Fatalf("not restored: engines=%v", s.ListEngines())
	}
	if st, _ := grantStatus(bus, "engine", "crush", "crush"); st != capability.StatusPublished {
		t.Fatalf("grant status after reload = %q", st)
	}

	// Close() shuts the host down; later calls are harmless.
	s.Close()
	s.Close()
	if workflow.NodeTypeAvailable("coding_agent") || len(s.ListEngines()) != 0 {
		t.Fatalf("shutdown left contributions behind: %v", s.ListEngines())
	}
}

func TestPluginsAbsentWhenNoCrushBinary(t *testing.T) {
	t.Setenv(crushEnvExe, os.DevNull+"-missing")
	s := NewAppService(&Runtime{capBus: capability.NewBus()})
	defer s.Close()
	for _, st := range s.ListPlugins() {
		if st.Name == "crush" {
			t.Fatalf("crush plugin must not exist without a binary: %+v", st)
		}
	}
	if workflow.NodeTypeAvailable("coding_agent") {
		t.Fatal("coding_agent node leaked without the plugin")
	}
}
