package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
	"agentgo/internal/tools"
)

type invokeSubagentInput struct {
	Task      string `json:"task"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
}

type runSwarmInput struct {
	Topic    string   `json:"topic"`
	AgentIDs []string `json:"agent_ids,omitempty"`
}
type runSwarmOutput struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

type invokeSubagentOutput struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// RegisterSubagentTool adds invoke_subagent without import cycle.
func RegisterSubagentTool(r *tools.Registry, runner *Runner, llm func() LLMSettings, sessionID func(context.Context) string) error {
	if runner == nil || r == nil {
		return nil
	}
	t, err := utils.InferTool("invoke_subagent",
		"Delegate a sub-task to a child agent with a narrower focus.",
		func(ctx context.Context, in invokeSubagentInput) (invokeSubagentOutput, error) {
			task := strings.TrimSpace(in.Task)
			if task == "" {
				return invokeSubagentOutput{Error: "task required"}, nil
			}
			ctx = governance.WithSubagentDepth(ctx, governance.SubagentDepth(ctx)+1)
			var profile SubagentDef
			if runner.subagentReg != nil {
				key := strings.TrimSpace(in.AgentID)
				if key == "" {
					key = strings.TrimSpace(in.AgentName)
				}
				if key != "" {
					if d, ok, _ := runner.subagentReg.Get(ctx, key); ok {
						profile = d
					}
				}
			}
			out, err := runner.RunSubagentProfile(ctx, llm(), sessionID(ctx), profile, task)
			if err != nil {
				return invokeSubagentOutput{Error: err.Error()}, nil
			}
			return invokeSubagentOutput{Result: out}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	t2, err := utils.InferTool("run_swarm",
		"Run a society-of-mind swarm: parallel registered subagents on one topic (Eino compose fan-out).",
		func(ctx context.Context, in runSwarmInput) (runSwarmOutput, error) {
			topic := strings.TrimSpace(in.Topic)
			if topic == "" {
				return runSwarmOutput{Error: "topic required"}, nil
			}
			out, err := runner.RunSwarm(ctx, llm(), sessionID(ctx), SwarmRequest{Topic: topic, AgentIDs: in.AgentIDs})
			if err != nil {
				return runSwarmOutput{Error: err.Error()}, nil
			}
			return runSwarmOutput{Result: out}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t2)
	return nil
}
