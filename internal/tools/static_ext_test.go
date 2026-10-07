package tools

import "testing"

func TestRegisterStaticToolLifecycle(t *testing.T) {
	if IsStaticTool("x_plugin_tool") {
		t.Fatal("must not be static before registration")
	}
	dispose := RegisterStaticTool("x_plugin_tool", true)
	if !IsStaticTool("x_plugin_tool") {
		t.Fatal("should be static after registration")
	}
	if m, ok := PluginStaticTools()["x_plugin_tool"]; !ok || !m {
		t.Fatalf("mutating flag lost: %v %v", m, ok)
	}
	dispose()
	dispose() // idempotent
	if IsStaticTool("x_plugin_tool") {
		t.Fatal("must be dynamic again after dispose")
	}
}

func TestStaticToolStaleDisposeKeepsNewerRegistration(t *testing.T) {
	d1 := RegisterStaticTool("x_re", false)
	d2 := RegisterStaticTool("x_re", true) // reload re-registers before old dispose runs
	d1()
	if !IsStaticTool("x_re") {
		t.Fatal("stale dispose removed the newer registration")
	}
	d2()
	if IsStaticTool("x_re") {
		t.Fatal("still static after final dispose")
	}
}
