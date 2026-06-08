package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/compose"
)

// SwarmRequest is input for parallel multi-subagent orchestration.
type SwarmRequest struct {
	Topic    string   `json:"topic"`
	AgentIDs []string `json:"agent_ids,omitempty"`
}

// SwarmPart is one subagent outcome in the swarm graph.
type SwarmPart struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Role      string `json:"role"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
}

// RunSwarm executes registered subagents in parallel via an Eino compose graph (fan-out / fan-in).
func (r *Runner) RunSwarm(ctx context.Context, cfg LLMSettings, sessionID string, req SwarmRequest) (string, error) {
	if r == nil || r.subagentReg == nil {
		return "", fmt.Errorf("swarm: subagent registry unavailable")
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return "", fmt.Errorf("swarm: topic required")
	}
	agents, err := r.subagentReg.ResolveIDs(ctx, req.AgentIDs)
	if err != nil {
		return "", err
	}
	if len(agents) == 0 {
		return "", fmt.Errorf("swarm: no enabled subagents")
	}
	runnable, err := buildSwarmRunnable(r, cfg, sessionID, agents, topic)
	if err != nil {
		return "", err
	}
	return runnable.Invoke(ctx, SwarmRequest{Topic: topic, AgentIDs: req.AgentIDs})
}

func buildSwarmRunnable(r *Runner, cfg LLMSettings, sessionID string, agents []SubagentDef, topic string) (compose.Runnable[SwarmRequest, string], error) {
	g := compose.NewGraph[SwarmRequest, string]()
	agentSnap := append([]SubagentDef(nil), agents...)
	runner := r
	llm := cfg
	sid := sessionID
	subject := topic

	err := g.AddLambdaNode("swarm_parallel", compose.InvokableLambda(func(ctx context.Context, _ SwarmRequest) (string, error) {
		parts := make([]SwarmPart, len(agentSnap))
		var wg sync.WaitGroup
		for i, ag := range agentSnap {
			wg.Add(1)
			go func(i int, ag SubagentDef) {
				defer wg.Done()
				parts[i].AgentID = ag.ID
				parts[i].AgentName = ag.Name
				parts[i].Role = ag.Role
				task := fmt.Sprintf("[蜂群议题] %s\n请以「%s」身份完成子任务。", subject, ag.Role)
				out, err := runner.RunSubagentProfile(ctx, llm, sid, ag, task)
				if err != nil {
					parts[i].Error = err.Error()
					return
				}
				parts[i].Output = out
			}(i, ag)
		}
		wg.Wait()
		var b strings.Builder
		b.WriteString("## Swarm 汇总\n")
		b.WriteString("议题：")
		b.WriteString(subject)
		b.WriteString("\n\n")
		for _, p := range parts {
			b.WriteString("### ")
			b.WriteString(p.AgentName)
			if p.Role != "" {
				b.WriteString(" (")
				b.WriteString(p.Role)
				b.WriteString(")")
			}
			b.WriteString("\n")
			if p.Error != "" {
				b.WriteString("错误: ")
				b.WriteString(p.Error)
			} else {
				b.WriteString(p.Output)
			}
			b.WriteString("\n\n")
		}
		return strings.TrimSpace(b.String()), nil
	}))
	if err != nil {
		return nil, err
	}
	_ = g.AddEdge(compose.START, "swarm_parallel")
	_ = g.AddEdge("swarm_parallel", compose.END)
	return g.Compile(context.Background())
}
