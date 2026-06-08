package capability

const (
	EventWorkflowDone     = "workflow.done"
	EventToolCompiled     = "tool.compiled"
	EventAppRuntimeError  = "app.runtime_error"
	EventCapabilityCompiled = "capability.compiled"
)

// Event is a broadcast artifact for App Matrix / multi-agent orchestration (L5).
type Event struct {
	Type      string            `json:"type"` // workflow.done, app.registered, tool.compiled, agent.message
	Source    string            `json:"source"`
	SessionID string            `json:"session_id,omitempty"`
	Payload   map[string]string `json:"payload,omitempty"`
}

// Subscriber receives capability bus events.
type Subscriber func(Event)

// Bus with event fan-out (in addition to grant registry).
func (b *Bus) Subscribe(fn Subscriber) {
	if b == nil || fn == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers = append(b.subscribers, fn)
}

// Publish notifies all subscribers (non-blocking).
func (b *Bus) Publish(ev Event) {
	if b == nil {
		return
	}
	b.mu.RLock()
	subs := append([]Subscriber(nil), b.subscribers...)
	b.mu.RUnlock()
	for _, fn := range subs {
		fn(ev)
	}
}

// PublishWorkflowDone broadcasts workflow output for app_matrix consumers.
func (b *Bus) PublishWorkflowDone(sessionID, workflowID, output string) {
	b.Publish(Event{
		Type: EventWorkflowDone, Source: workflowID, SessionID: sessionID,
		Payload: map[string]string{"output": output},
	})
}

// PublishAppRuntimeError broadcasts inner-app failures for admin self-heal.
func (b *Bus) PublishAppRuntimeError(appName, errMsg string) {
	b.Publish(Event{
		Type: EventAppRuntimeError, Source: appName,
		Payload: map[string]string{"error": errMsg},
	})
}

// PublishToolCompiled announces a new dynamic tool for other agents.
func (b *Bus) PublishToolCompiled(name, scope string) {
	b.Publish(Event{
		Type: "tool.compiled", Source: name,
		Payload: map[string]string{"scope": scope},
	})
}
