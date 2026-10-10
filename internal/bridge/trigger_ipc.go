package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/tools"
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

// --- tools.WatchHandler: lets the agent arm triggers that wake its own session ---

const maxArmedPerSession = 20

func (r *Runtime) CreateWatch(_ context.Context, req tools.WatchRequest) (any, error) {
	e, err := r.triggerEngine()
	if err != nil {
		return nil, err
	}
	existing, err := e.List()
	if err != nil {
		return nil, err
	}
	armed := 0
	for _, t := range existing {
		if t.SessionID == req.SessionID && t.Status == trigger.StatusArmed {
			armed++
		}
	}
	if armed >= maxArmedPerSession {
		return nil, fmt.Errorf("too many active triggers in this session (%d); cancel some first", armed)
	}
	t := trigger.Trigger{
		Kind: trigger.Kind(req.Kind), PID: req.PID, Path: req.Path, Pattern: req.Pattern,
		Title: req.Title, Action: trigger.ActionWakeSession, SessionID: req.SessionID, Prompt: req.Prompt,
	}
	if req.ExpiresInSec > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(req.ExpiresInSec) * time.Second).Unix()
	}
	return e.Add(t)
}

func (r *Runtime) ListWatches(_ context.Context, sessionID string) (any, error) {
	e, err := r.triggerEngine()
	if err != nil {
		return nil, err
	}
	all, err := e.List()
	if err != nil {
		return nil, err
	}
	out := []trigger.Trigger{}
	for _, t := range all {
		if t.SessionID == sessionID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *Runtime) CancelWatch(_ context.Context, sessionID, id string) error {
	e, err := r.triggerEngine()
	if err != nil {
		return err
	}
	t, err := e.Get(id)
	if err != nil {
		return err
	}
	if t.SessionID != sessionID {
		return errors.New("trigger belongs to another session")
	}
	return e.Cancel(id)
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
