package bridge

import (
	"context"
	"fmt"
	"strings"
)

// RunWorkflow implements tools.InnerAppRunner.
func (r *Runtime) RunWorkflow(ctx context.Context, workflowID, input string) (string, error) {
	return r.executeWorkflow(ctx, workflowID, input)
}

// RunAgentPrompt implements tools.InnerAppRunner (single-turn inner agent app).
func (r *Runtime) RunAgentPrompt(ctx context.Context, sessionID, systemPrompt, userInput string) (string, error) {
	runner := r.agentRunner
	if runner == nil {
		return "", fmt.Errorf("agent runner unavailable")
	}
	cfg := r.AgentLLMSettings()
	if cfg.APIKey == "" {
		return "", fmt.Errorf("API key not configured")
	}
	prompt := strings.TrimSpace(userInput)
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		prompt = "[Inner App Instructions]\n" + sp + "\n\n[Task]\n" + prompt
	}
	res, err := runner.Generate(ctx, cfg, sessionID, prompt, nil)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return res.Content, nil
}
