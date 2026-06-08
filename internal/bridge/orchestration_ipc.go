package bridge

import (
	"context"
	"encoding/json"
	"strings"

	"agentgo/internal/agent"
)

func (s *AppService) ListSubagents(limit int) []agent.SubagentDef {
	reg := s.rt.subagentRegistry()
	if reg == nil {
		return nil
	}
	list, _ := reg.List(context.Background(), limit)
	return list
}

func (s *AppService) UpsertSubagent(defJSON string) map[string]any {
	reg := s.rt.subagentRegistry()
	if reg == nil {
		return map[string]any{"success": false, "error": "subagent registry unavailable"}
	}
	var d agent.SubagentDef
	if err := json.Unmarshal([]byte(defJSON), &d); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	out, err := reg.Upsert(context.Background(), d)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "subagent": out}
}

func (s *AppService) DeleteSubagent(idOrName string) map[string]any {
	reg := s.rt.subagentRegistry()
	if reg == nil {
		return map[string]any{"success": false}
	}
	if err := reg.Delete(context.Background(), idOrName); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

func (s *AppService) SeedSubagents() map[string]any {
	reg := s.rt.subagentRegistry()
	if reg == nil {
		return map[string]any{"success": false, "error": "subagent registry unavailable"}
	}
	n, err := reg.SeedDefaults(context.Background())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "seeded": n}
}

func (s *AppService) RunSwarm(sessionID, topic, agentIDsJSON string) map[string]any {
	runner := s.rt.AgentRunner()
	if runner == nil {
		return map[string]any{"success": false, "error": "runner unavailable"}
	}
	var ids []string
	if strings.TrimSpace(agentIDsJSON) != "" {
		_ = json.Unmarshal([]byte(agentIDsJSON), &ids)
	}
	cfg := s.rt.LLMConfig()
	llm := agent.LLMSettings{APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model}
	out, err := runner.RunSwarm(context.Background(), llm, sessionID, agent.SwarmRequest{
		Topic: topic, AgentIDs: ids,
	})
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "result": out}
}

func (s *AppService) StartSwarmTask(sessionID, topic, agentIDsJSON string) map[string]any {
	if s.rt.taskHub == nil {
		return map[string]any{"success": false, "error": "task hub unavailable"}
	}
	payload, _ := json.Marshal(map[string]any{
		"topic": topic, "agent_ids_json": agentIDsJSON,
	})
	t, err := s.rt.taskHub.Start("swarm", sessionID, string(payload))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "task": t}
}

func (r *Runtime) subagentRegistry() *agent.SubagentRegistry {
	if r == nil || r.agentRunner == nil {
		return nil
	}
	return r.agentRunner.SubagentRegistry()
}
