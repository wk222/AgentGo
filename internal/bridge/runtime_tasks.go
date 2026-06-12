package bridge

import (
	"context"
	"encoding/json"

	"agentgo/internal/agent"
	"agentgo/internal/taskhub"
)

func (r *Runtime) runBackgroundTask(ctx context.Context, t taskhub.Task, emit func(taskhub.Event)) error {
	emit(taskhub.Event{Type: taskhub.EventStatus, Payload: `{"status":"running"}`})
	cfg := r.LLMConfig()
	runner := r.AgentRunner()
	if runner == nil || cfg.APIKey == "" {
		emit(taskhub.Event{Type: taskhub.EventError, Payload: `{"error":"LLM not configured"}`})
		return nil
	}
	llm := agent.LLMSettings{APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model}
	if t.Kind == "swarm" {
		var payload struct {
			Topic        string `json:"topic"`
			AgentIDsJSON string `json:"agent_ids_json"`
		}
		_ = json.Unmarshal([]byte(t.Input), &payload)
		var ids []string
		if payload.AgentIDsJSON != "" {
			_ = json.Unmarshal([]byte(payload.AgentIDsJSON), &ids)
		}
		out, err := runner.RunSwarm(ctx, llm, t.SessionID, agent.SwarmRequest{Topic: payload.Topic, AgentIDs: ids})
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			emit(taskhub.Event{Type: taskhub.EventError, Payload: string(b)})
			return err
		}
		b, _ := json.Marshal(map[string]string{"result": out})
		emit(taskhub.Event{Type: taskhub.EventDone, Payload: string(b)})
		return nil
	}
	var full string
	_, err := runner.GenerateStream(ctx, llm, t.SessionID, t.Input, nil, func(delta string) {
		full += delta
		b, _ := json.Marshal(map[string]string{"delta": delta})
		emit(taskhub.Event{Type: taskhub.EventChunk, Payload: string(b)})
	})

	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		emit(taskhub.Event{Type: taskhub.EventError, Payload: string(b)})
		return err
	}
	b, _ := json.Marshal(map[string]string{"result": full})
	emit(taskhub.Event{Type: taskhub.EventDone, Payload: string(b)})
	return nil
}
