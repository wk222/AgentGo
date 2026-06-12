package workflow

import (
	"context"
	"fmt"
	"strings"
)

// DebateNodeExecutor runs multi-perspective LLM rounds and merges opinions.
type DebateNodeExecutor struct{}

func (e *DebateNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.LLMGenerate == nil {
		return "", fmt.Errorf("debate node %s: no LLM", n.ID)
	}
	topic := expandTemplateVars(n.Prompt, input, last, rc.Vars)
	if topic == "" {
		topic = last
	}
	roles := []string{"支持者", "质疑者", "总结者"}
	if n.Config != nil {
		if raw, ok := n.Config["roles"].(string); ok && raw != "" {
			roles = strings.Split(raw, ",")
		}
	}
	rounds := 1
	if n.Config != nil {
		switch v := n.Config["rounds"].(type) {
		case float64:
			rounds = int(v)
		case int:
			rounds = v
		}
	}
	if rounds < 1 {
		rounds = 1
	}
	var buf strings.Builder
	buf.WriteString("## Debate\n")
	// transcript accumulates prior arguments so later speakers and rounds respond
	// to what was already said instead of arguing in isolation.
	var transcript strings.Builder
	for r := 0; r < rounds; r++ {
		for _, role := range roles {
			role = strings.TrimSpace(role)
			if role == "" {
				continue
			}
			var prompt string
			if transcript.Len() == 0 {
				prompt = fmt.Sprintf("[辩论轮次 %d · %s]\n议题：%s\n请从该视角给出要点（简洁）。", r+1, role, topic)
			} else {
				prompt = fmt.Sprintf("[辩论轮次 %d · %s]\n议题：%s\n\n此前的发言记录：\n%s\n请以「%s」的视角针对上面的发言进行回应、反驳或补充（简洁，不要重复已有论点）。", r+1, role, topic, strings.TrimSpace(transcript.String()), role)
			}
			out, err := rc.LLMGenerate(ctx, prompt)
			if err != nil {
				return "", fmt.Errorf("debate node %s: %w", n.ID, err)
			}
			buf.WriteString(fmt.Sprintf("\n### 轮次 %d · %s\n%s\n", r+1, role, out))
			transcript.WriteString(fmt.Sprintf("【轮次 %d · %s】%s\n", r+1, role, strings.TrimSpace(out)))
		}
	}
	return buf.String(), nil
}

// CronNodeExecutor registers a scheduled job (interval spec in node config).
type CronNodeExecutor struct{}

func (e *CronNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.ScheduleCron == nil {
		return "", fmt.Errorf("cron node %s: scheduler unavailable", n.ID)
	}
	spec := "every 1h"
	prompt := expandTemplateVars(n.Prompt, input, last, rc.Vars)
	if prompt == "" {
		prompt = last
	}
	sid := ""
	if n.Config != nil {
		if s, ok := n.Config["spec"].(string); ok && s != "" {
			spec = s
		}
		if p, ok := n.Config["prompt"].(string); ok && p != "" {
			prompt = expandTemplateVars(p, input, last, rc.Vars)
		}
		if s, ok := n.Config["session_id"].(string); ok {
			sid = s
		}
	}
	id, err := rc.ScheduleCron(ctx, spec, prompt, sid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"scheduled_job_id":%q,"spec":%q}`, id, spec), nil
}
