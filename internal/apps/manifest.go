package apps

import "strings"

// ActionManifest describes a callable surface that an Inner App UI may invoke.
type ActionManifest struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Source          string `json:"source,omitempty"`
	Binding         string `json:"binding,omitempty"`
	RequiresPayload bool   `json:"requires_payload,omitempty"`
}

// AppManifest is the host-visible contract for an Inner App.
type AppManifest struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Kind         string           `json:"kind"`
	Enabled      bool             `json:"enabled"`
	HasUI        bool             `json:"has_ui"`
	BundlePath   string           `json:"bundle_path,omitempty"`
	WorkflowID   string           `json:"workflow_id,omitempty"`
	Exports      []string         `json:"exports,omitempty"`
	ExportPolicy string           `json:"export_policy"`
	Actions      []ActionManifest `json:"actions"`
}

// ManifestFor derives an explicit action contract without requiring a DB schema
// migration. Apps with no exports are treated as legacy/open, but still receive
// a visible manifest of known built-ins.
func ManifestFor(app InnerApp) AppManifest {
	policy := "exports"
	if len(app.Exports) == 0 {
		policy = "open"
	}
	m := AppManifest{
		Name:         app.Name,
		Description:  app.Description,
		Kind:         strings.ToLower(strings.TrimSpace(app.Kind)),
		Enabled:      app.Enabled,
		HasUI:        app.Kind == "ui" || strings.TrimSpace(app.BundlePath) != "",
		BundlePath:   app.BundlePath,
		WorkflowID:   app.WorkflowID,
		Exports:      append([]string(nil), app.Exports...),
		ExportPolicy: policy,
	}

	add := func(name, source, binding string, requiresPayload bool) {
		name = strings.TrimSpace(name)
		if name == "" || hasAction(m.Actions, name) {
			return
		}
		m.Actions = append(m.Actions, ActionManifest{
			Name:            name,
			Description:     actionDescription(name),
			Source:          source,
			Binding:         binding,
			RequiresPayload: requiresPayload,
		})
	}

	add("info", "builtin", "", false)
	if len(app.Exports) > 0 {
		for _, e := range app.Exports {
			binding := ""
			if strings.EqualFold(strings.TrimSpace(e), "workflow_run") || strings.EqualFold(strings.TrimSpace(e), "run") {
				binding = app.WorkflowID
			}
			add(e, "export", binding, true)
		}
		return m
	}

	add("ping", "builtin", "", false)
	add("echo", "builtin", "", true)
	if strings.TrimSpace(app.WorkflowID) != "" {
		add("workflow_run", "derived", app.WorkflowID, true)
		add("run", "derived", app.WorkflowID, true)
	}
	if strings.TrimSpace(app.SystemPrompt) != "" {
		add("chat", "derived", "agent", true)
	}
	return m
}

func hasAction(actions []ActionManifest, name string) bool {
	for _, a := range actions {
		if strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

func actionDescription(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "info":
		return "Read app metadata"
	case "ping":
		return "Health check"
	case "echo":
		return "Echo payload"
	case "workflow_run", "run":
		return "Run bound workflow"
	case "chat":
		return "Send payload to bound agent prompt"
	default:
		return "Exported app action"
	}
}
