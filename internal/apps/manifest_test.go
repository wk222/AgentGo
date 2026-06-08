package apps

import "testing"

func TestManifestForExportedUIApp(t *testing.T) {
	app := InnerApp{
		Name:       "demo",
		Kind:       "ui",
		Enabled:    true,
		WorkflowID: "wf1",
		Exports:    []string{"ping", "workflow_run"},
	}
	m := ManifestFor(app)
	if m.ExportPolicy != "exports" {
		t.Fatalf("policy=%q, want exports", m.ExportPolicy)
	}
	if !hasAction(m.Actions, "ping") || !hasAction(m.Actions, "workflow_run") {
		t.Fatalf("missing exported actions: %+v", m.Actions)
	}
	if hasAction(m.Actions, "echo") {
		t.Fatalf("unexpected unexported action in strict manifest: %+v", m.Actions)
	}
}

func TestManifestForLegacyUIAppDerivesBuiltins(t *testing.T) {
	app := InnerApp{Name: "legacy", Kind: "ui", Enabled: true, WorkflowID: "wf1"}
	m := ManifestFor(app)
	if m.ExportPolicy != "open" {
		t.Fatalf("policy=%q, want open", m.ExportPolicy)
	}
	for _, action := range []string{"info", "ping", "echo", "workflow_run", "run"} {
		if !hasAction(m.Actions, action) {
			t.Fatalf("missing %s in %+v", action, m.Actions)
		}
	}
}
