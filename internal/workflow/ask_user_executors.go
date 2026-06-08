package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
)

// AskUserNodeExecutor pauses the compose graph for human input (Eino StatefulInterrupt + ResumeWithData).
type AskUserNodeExecutor struct{}

func (e *AskUserNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	prompt := strings.TrimSpace(n.Prompt)
	if prompt == "" && n.Config != nil {
		if s, ok := n.Config["prompt"].(string); ok {
			prompt = strings.TrimSpace(s)
		}
	}
	if prompt == "" {
		prompt = strings.TrimSpace(last)
	}
	if prompt == "" {
		return "", fmt.Errorf("ask_user node %s: prompt required", n.ID)
	}
	if rc.CheckPointStore == nil {
		return "", fmt.Errorf("ask_user node %s: checkpoint store required (enable AGENTGO_WORKFLOW_CHECKPOINT)", n.ID)
	}
	wasInterrupted, hasState, _ := compose.GetInterruptState[askUserState](ctx)
	if wasInterrupted && hasState {
		if isResume, hasData, rd := compose.GetResumeContext[waitResumeData](ctx); isResume && hasData && rd.Payload != "" {
			return rd.Payload, nil
		}
		if isResume, hasData, s := compose.GetResumeContext[string](ctx); isResume && hasData && s != "" {
			return s, nil
		}
		if isResume, hasData, m := compose.GetResumeContext[map[string]any](ctx); isResume && hasData {
			if a, ok := m["answer"].(string); ok && a != "" {
				return a, nil
			}
			if p, ok := m["payload"].(string); ok && p != "" {
				return p, nil
			}
		}
	}
	return "", compose.StatefulInterrupt(ctx,
		map[string]any{"kind": "ask_user", "prompt": prompt},
		askUserState{Prompt: prompt},
	)
}
