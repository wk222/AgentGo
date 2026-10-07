package bridge

import "agentgo/internal/plugin"

// Plugins exposes the plugin host (nil-safe accessors live on AppService).
func (s *AppService) Plugins() *plugin.Host { return s.plugins }

// ListPlugins reports every plugin's state, dependencies and last error.
func (s *AppService) ListPlugins() []plugin.Status {
	if s.plugins == nil {
		return []plugin.Status{}
	}
	return s.plugins.Statuses()
}

// PluginGraph is the actual dependency graph of the plugin host: every plugin
// with its state and failure reason, and every declared need with who provides
// it and whether it is available now. Frontends use it to show what runs and
// what is missing instead of guessing from a static list.
func (s *AppService) PluginGraph() plugin.Graph {
	if s.plugins == nil {
		return plugin.Graph{Nodes: []plugin.Status{}, Edges: []plugin.Edge{}}
	}
	return s.plugins.Graph()
}

// ReloadPlugin restarts a plugin and everything that depends on it (also retries failed ones).
func (s *AppService) ReloadPlugin(name string) map[string]any {
	return s.pluginOp(name, s.plugins.Reload)
}

// StopPlugin stops a plugin and its dependents; it stays off until ReloadPlugin.
func (s *AppService) StopPlugin(name string) map[string]any {
	return s.pluginOp(name, s.plugins.Stop)
}

func (s *AppService) pluginOp(name string, op func(string) error) map[string]any {
	if s.plugins == nil {
		return map[string]any{"success": false, "error": "plugin host unavailable"}
	}
	if err := op(name); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	st, _ := s.plugins.Status(name)
	return map[string]any{"success": true, "status": st}
}
