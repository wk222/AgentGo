package tools

import (
	"context"
	"fmt"
)

// SetDynamicSandbox sets the directory used to materialize Python scripts for compiled tools.
func (r *Registry) SetDynamicSandbox(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dynamicSandbox = dir
}

// SyncDynamicFromStore loads all persisted tools as individual BaseTool entries (tool_search visible).
func (r *Registry) SyncDynamicFromStore(store *DynamicStore) error {
	if store == nil {
		return nil
	}
	list, err := store.List()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.dynamicCompiled {
		delete(r.tools, name)
	}
	r.dynamicCompiled = make(map[string]bool)
	if r.dynamicSandbox == "" {
		return fmt.Errorf("dynamic sandbox dir not configured")
	}
	for _, def := range list {
		if err := ParseParametersJSON(def.Parameters); err != nil {
			continue
		}
		t, err := NewPythonDynamicTool(def, r.dynamicSandbox)
		if err != nil {
			continue
		}
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		r.tools[info.Name] = t
		r.dynamicCompiled[info.Name] = true
	}
	return nil
}

// RegisterDynamicTool compiles and registers one tool immediately after create_tool.
func (r *Registry) RegisterDynamicTool(def DynamicToolDef) error {
	if err := ParseParametersJSON(def.Parameters); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dynamicSandbox == "" {
		return fmt.Errorf("dynamic sandbox dir not configured")
	}
	t, err := NewPythonDynamicTool(def, r.dynamicSandbox)
	if err != nil {
		return err
	}
	info, err := t.Info(context.Background())
	if err != nil || info == nil {
		return fmt.Errorf("tool info: %w", err)
	}
	r.tools[info.Name] = t
	if r.dynamicCompiled == nil {
		r.dynamicCompiled = make(map[string]bool)
	}
	r.dynamicCompiled[info.Name] = true
	return nil
}

// IsDynamicCompiled reports whether name is a hot-loaded dynamic Python tool.
func (r *Registry) IsDynamicCompiled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dynamicCompiled[name]
}

// DynamicCompiledNames returns hot-loaded dynamic Python tool names.
func (r *Registry) DynamicCompiledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.dynamicCompiled))
	for name := range r.dynamicCompiled {
		out = append(out, name)
	}
	return out
}
