package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentgo/internal/trigger"
	"agentgo/internal/workflow"
)

// fireTrigger performs a fired trigger's action. wake_session starts a background agent turn
// in the original session, with the event attached so the agent knows what happened.
func (r *Runtime) fireTrigger(_ context.Context, t trigger.Trigger, ev trigger.Event) error {
	evJSON, _ := json.Marshal(ev.Detail)
	switch t.Action {
	case trigger.ActionWakeSession:
		if r.taskHub == nil {
			return errors.New("task hub unavailable")
		}
		var b strings.Builder
		if p := strings.TrimSpace(t.Prompt); p != "" {
			b.WriteString(p)
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[事件触发 %s · %s] %s", t.ID, t.Kind, evJSON)
		_, err := r.taskHub.Start("trigger", t.SessionID, b.String())
		return err
	case trigger.ActionEmitSignal:
		workflow.DefaultSignalBus().Emit(t.RunID, t.Signal, string(evJSON))
		return nil
	}
	return fmt.Errorf("unknown trigger action %q", t.Action)
}

func (r *Runtime) triggerEngine() (*trigger.Engine, error) {
	if r.triggers == nil {
		return nil, errors.New("trigger engine is disabled")
	}
	return r.triggers, nil
}

// --- gateway.TriggerBackend ---

func (g *RuntimeGateway) ListTriggers(context.Context) ([]byte, error) {
	e, err := g.rt.triggerEngine()
	if err != nil {
		return nil, err
	}
	list, err := e.List()
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []trigger.Trigger{}
	}
	return json.Marshal(list)
}

func (g *RuntimeGateway) CreateTrigger(_ context.Context, body []byte) ([]byte, error) {
	e, err := g.rt.triggerEngine()
	if err != nil {
		return nil, err
	}
	var t trigger.Trigger
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, err
	}
	created, err := e.Add(t)
	if err != nil {
		return nil, err
	}
	return json.Marshal(created)
}

func (g *RuntimeGateway) GetTrigger(_ context.Context, id string) ([]byte, error) {
	e, err := g.rt.triggerEngine()
	if err != nil {
		return nil, err
	}
	t, err := e.Get(id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(t)
}

func (g *RuntimeGateway) CancelTrigger(_ context.Context, id string) error {
	e, err := g.rt.triggerEngine()
	if err != nil {
		return err
	}
	return e.Cancel(id)
}

func (g *RuntimeGateway) FireTrigger(_ context.Context, id string, payload map[string]any) error {
	e, err := g.rt.triggerEngine()
	if err != nil {
		return err
	}
	return e.FireWebhook(id, payload)
}

func (g *RuntimeGateway) EmitSignal(_ context.Context, runID, name, payload string) error {
	workflow.DefaultSignalBus().Emit(runID, name, payload)
	return nil
}
