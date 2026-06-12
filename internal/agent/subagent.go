package agent

import (
	"context"
	"fmt"
	"strings"
)

// SubagentDelegate runs a focused sub-task (PyBot SubagentRuntime subset).
type SubagentDelegate interface {
	RunSubagent(ctx context.Context, cfg LLMSettings, sessionID, task string) (string, error)
}

// RunSubagent executes a shorter agent loop with a task-only prompt.
func (r *Runner) RunSubagent(ctx context.Context, cfg LLMSettings, sessionID, task string) (string, error) {
	return r.RunSubagentProfile(ctx, cfg, sessionID, SubagentDef{}, task)
}

// RunSubagentProfile runs a sub-task with an optional registered profile (role/system prompt).
func (r *Runner) RunSubagentProfile(ctx context.Context, cfg LLMSettings, sessionID string, profile SubagentDef, task string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("empty subagent task")
	}
	var prompt string
	if strings.TrimSpace(profile.SystemPrompt) != "" {
		prompt = fmt.Sprintf("%s\n\n[子任务]\n%s\n请专注完成，简洁汇报。", strings.TrimSpace(profile.SystemPrompt), task)
	} else {
		prompt = fmt.Sprintf("[子智能体任务] %s\n请专注完成上述子任务，简洁汇报结果。", task)
	}
	if hint := strings.TrimSpace(profile.ModelHint); hint != "" && cfg.Model == "" {
		cfg.Model = hint
	}
	sid := sessionID
	if profile.Name != "" {
		sid = sessionID + ":sub:" + profile.Name
	}
	res, err := r.Generate(ctx, cfg, sid, prompt, nil)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// SetSubagentRegistry attaches the persisted subagent catalog.
func (r *Runner) SetSubagentRegistry(reg *SubagentRegistry) {
	r.subagentReg = reg
}

// SubagentRegistry returns the registry when configured.
func (r *Runner) SubagentRegistry() *SubagentRegistry {
	return r.subagentReg
}
