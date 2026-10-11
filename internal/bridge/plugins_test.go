package bridge

import (
	"reflect"
	"testing"

	"agentgo/internal/capability"
	"agentgo/internal/plugin"
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
