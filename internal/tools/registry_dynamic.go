package tools

import (
	"context"
	"fmt"
	"log"
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
	for name := range r.dynamicCompiled {
		delete(r.tools, name)
	}
	r.dynamicCompiled = make(map[string]bool)
	sandbox := r.dynamicSandbox
	r.mu.Unlock()
	if sandbox == "" {
		return fmt.Errorf("dynamic sandbox dir not configured")
	}
	// A persisted definition that cannot be restored is logged by name and
	// reason, never silently dropped: the definition stays visible in
	// list_dynamic_tools, so the log is the only place the mismatch between
	// "stored" and "callable" can be diagnosed from.
	// Validation runs without r.mu held: it may spawn the Python interpreter.
	restored, skipped := 0, 0
	for _, def := range list {
		if _, exists := r.Get(def.Name); exists {
			continue // a built-in or already-registered tool wins; not an error
		}
		if err := ValidateDynamicToolDef(context.Background(), def); err != nil {
			log.Printf("tools: dynamic tool %q not restored: %v (definition kept in store; fix or delete it)", def.Name, err)
			skipped++
			continue
		}
		t, err := NewPythonDynamicTool(def, sandbox)
		if err != nil {
			log.Printf("tools: dynamic tool %q not restored: construct: %v", def.Name, err)
			skipped++
			continue
		}
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			log.Printf("tools: dynamic tool %q not restored: tool info: %v", def.Name, err)
			skipped++
			continue
		}
		r.mu.Lock()
		r.tools[info.Name] = t
		r.dynamicCompiled[info.Name] = true
		r.mu.Unlock()
		restored++
	}
	if skipped > 0 {
		log.Printf("tools: dynamic tool restore: %d restored, %d skipped (see lines above)", restored, skipped)
	}
	return nil
}

// RegisterDynamicTool validates and registers one tool immediately after create_tool.
// Validation includes parsing the Python body, so a tool that cannot run is
// rejected here instead of being announced as compiled.
func (r *Registry) RegisterDynamicTool(def DynamicToolDef) error {
	if err := ValidateDynamicToolDef(context.Background(), def); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[def.Name]; exists && !r.dynamicCompiled[def.Name] {
		return fmt.Errorf("cannot replace built-in tool %q", def.Name)
	}
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
	if !ValidToolName(info.Name) {
		return fmt.Errorf("invalid tool name %q: use only letters, digits, '_', '.', '-' (max 64 chars)", info.Name)
	}
	r.tools[info.Name] = t
	if r.dynamicCompiled == nil {
		r.dynamicCompiled = make(map[string]bool)
	}
	r.dynamicCompiled[info.Name] = true
	return nil
}

// RemoveDynamicTool unregisters a hot-loaded generated tool. It reports whether
// anything was removed; built-in tools are never touched.
func (r *Registry) RemoveDynamicTool(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dynamicCompiled[name] {
		return false
	}
	delete(r.tools, name)
	delete(r.dynamicCompiled, name)
	return true
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
