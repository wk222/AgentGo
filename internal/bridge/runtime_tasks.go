package bridge

import (
	"context"
	"encoding/json"
	"log"
	"strings"

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
	llm := cfg.agentSettings()
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
	// A trigger wake-up happens inside a real conversation: record the event and the agent's reply in
	// that session so the user sees them in the chat history, not only in the task event log.
	sid := t.SessionID
	if t.Kind == "cron" && sid == "" && r.Sessions() != nil {
		// A scheduled job with no conversation of its own opens a fresh one on every run.
		if s, err := r.Sessions().Create(ctx, "⏰ "+scheduledTitle(t.Input)); err == nil {
			sid = s.ID
		}
	}
	persist := (t.Kind == "trigger" || t.Kind == "wake" || t.Kind == "cron") && sid != "" && r.Sessions() != nil
	if persist {
		head := "⏰ 事件触发，正在唤醒智能体…"
		switch t.Kind {
		case "wake":
			head = "⏰ 有新的结果或消息，正在唤醒智能体…"
		case "cron":
			head = "⏰ 定时任务触发"
		}
		_ = r.Sessions().AppendMessage(ctx, sid, "system", head+"\n\n"+t.Input, "text", map[string]any{"source": t.Kind, "task_id": t.ID})
	}
	var full string
	res, err := runner.GenerateStream(ctx, llm, sid, t.Input, nil, func(delta string) {
		full += delta
		b, _ := json.Marshal(map[string]string{"delta": delta})
		emit(taskhub.Event{Type: taskhub.EventChunk, Payload: string(b)})
	})

	if err != nil {
		if persist {
			msg := "唤醒后执行失败: " + err.Error()
			if ctx.Err() != nil {
				msg = "这次唤醒被你停止了。"
			}
			_ = r.Sessions().AppendMessage(context.Background(), sid, "assistant", msg, "text", map[string]any{"source": t.Kind, "task_id": t.ID})
		}
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		emit(taskhub.Event{Type: taskhub.EventError, Payload: string(b)})
		return err
	}
	if persist {
		// The streamed deltas also carry raw tool output; prefer the model's final answer.
		answer := full
		if res != nil && strings.TrimSpace(res.Content) != "" {
			answer = res.Content
		}
		if strings.TrimSpace(answer) != "" {
			_ = r.Sessions().AppendMessage(context.Background(), sid, "assistant", answer, "text", map[string]any{"source": t.Kind, "task_id": t.ID})
		}
	}
	b, _ := json.Marshal(map[string]string{"result": full})
	emit(taskhub.Event{Type: taskhub.EventDone, Payload: string(b)})
	return nil
}

// reportStall tells the user about a session the watchdog gave up on. It is
// written into the conversation, so it is there whenever the user comes back.
func (r *Runtime) reportStall(a agent.WatchdogAction) {
	log.Printf("[watchdog] session %s stopped for good: %s", a.SessionID, a.Text)
	if r.Sessions() == nil || a.SessionID == "" {
		return
	}
	_ = r.Sessions().AppendMessage(context.Background(), a.SessionID, "system", "⚠️ "+a.Text, "text", map[string]any{"source": "watchdog"})
}
