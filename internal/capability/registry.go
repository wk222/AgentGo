package capability

import "time"

// SyncToolDTO is a generic DTO for syncing a tool asset.
type SyncToolDTO struct {
	Name        string
	Description string
	Scope       string
	RiskLevel   string
	Metadata    map[string]string
}

// SyncWorkflowDTO is a generic DTO for syncing a workflow asset.
type SyncWorkflowDTO struct {
	ID          string
	Name        string
	Description string
	Metadata    map[string]string
}

// SyncAppDTO is a generic DTO for syncing an inner app asset.
type SyncAppDTO struct {
	ID          string
	Name        string
	Description string
	Kind        string
	Metadata    map[string]string
}

// SyncAgentDTO is a generic DTO for syncing a sub-agent asset.
type SyncAgentDTO struct {
	ID           string
	Name         string
	Role         string
	SystemPrompt string
	Metadata     map[string]string
}

// SyncTools registers a batch of tools in the capability bus.
func (b *Bus) SyncTools(tools []SyncToolDTO) {
	if b == nil {
		return
	}
	for _, t := range tools {
		meta := t.Metadata
		if meta == nil {
			meta = make(map[string]string)
		}
		if t.Description != "" {
			meta["description"] = t.Description
		}
		g := b.Register("tool", t.Name, t.Scope, meta)
		if t.RiskLevel != "" {
			g.RiskLevel = t.RiskLevel
			b.mu.Lock()
			b.grants[g.ID] = g
			if b.store != nil {
				_ = b.store.Upsert(g)
			}
			b.mu.Unlock()
		}
	}
}

// SyncWorkflows registers a batch of workflows in the capability bus.
func (b *Bus) SyncWorkflows(workflows []SyncWorkflowDTO) {
	if b == nil {
		return
	}
	for _, w := range workflows {
		meta := w.Metadata
		if meta == nil {
			meta = make(map[string]string)
		}
		if w.Description != "" {
			meta["description"] = w.Description
		}
		b.Register("workflow", w.ID, "global", meta)
	}
}

// SyncApps registers a batch of inner apps in the capability bus.
func (b *Bus) SyncApps(apps []SyncAppDTO) {
	if b == nil {
		return
	}
	for _, app := range apps {
		meta := app.Metadata
		if meta == nil {
			meta = make(map[string]string)
		}
		if app.Description != "" {
			meta["description"] = app.Description
		}
		meta["app_kind"] = app.Kind
		b.Register("app", app.ID, "global", meta)
	}
}

// SyncAgents registers a batch of sub-agents in the capability bus.
func (b *Bus) SyncAgents(agents []SyncAgentDTO) {
	if b == nil {
		return
	}
	for _, ag := range agents {
		meta := ag.Metadata
		if meta == nil {
			meta = make(map[string]string)
		}
		if ag.Role != "" {
			meta["role"] = ag.Role
		}
		if ag.SystemPrompt != "" {
			meta["system_prompt"] = ag.SystemPrompt
		}
		b.Register("agent", ag.Name, "global", meta)
	}
}

// PublishCapabilityCompiled broadcasts a compile event.
func (b *Bus) PublishCapabilityCompiled(kind, name, scope string) {
	b.Publish(Event{
		Type: EventCapabilityCompiled,
		Source: name,
		Payload: map[string]string{
			"kind": kind,
			"scope": scope,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
}
