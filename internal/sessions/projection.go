package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// ProjectedRuntimeView implements PyBot §3.3 six-section session projection.
type ProjectedRuntimeView struct {
	SystemRules     []string
	DurableTasks    []string
	EpisodicSummary []string
	WorkflowContext []string
	WorkspaceRecent []string
	ContextHygiene  []string
	TeamMemory      []string
	Isolation       []string
	ActiveTurn      []FlatMessage
	ToolOutputs     []string
}

// Render compiles the projection into []*schema.Message for the chat model.
func (p *ProjectedRuntimeView) Render(ctx context.Context) ([]*schema.Message, error) {
	_ = ctx
	var msgs []*schema.Message
	var sys strings.Builder

	writeBlock := func(tag, title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		sys.WriteString("<")
		sys.WriteString(tag)
		sys.WriteString(">\n")
		if title != "" {
			sys.WriteString(title)
			sys.WriteString("\n")
		}
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			sys.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				sys.WriteString("\n")
			}
		}
		sys.WriteString("</")
		sys.WriteString(tag)
		sys.WriteString(">\n\n")
	}

	writeBlock("system_rules", "", p.SystemRules)
	writeBlock("durable_tasks", "当前管理员长效任务：", p.DurableTasks)
	writeBlock("episodic_summary", "历史浓缩记忆：", p.EpisodicSummary)
	writeBlock("workflow_context", "", p.WorkflowContext)
	writeBlock("workspace", "近期工作区上下文：", p.WorkspaceRecent)
	writeBlock("context_hygiene", "", p.ContextHygiene)
	writeBlock("team_memory", "", p.TeamMemory)
	writeBlock("isolation", "", p.Isolation)

	if s := strings.TrimSpace(sys.String()); s != "" {
		msgs = append(msgs, schema.SystemMessage(s))
	}

	for _, m := range p.ActiveTurn {
		role := m.Role
		switch {
		case role == string(schema.User) || role == "user":
			um := schema.UserMessage(m.Content)
			if len(m.UserInputMultiContent) > 0 {
				um.UserInputMultiContent = append([]schema.MessageInputPart(nil), m.UserInputMultiContent...)
			}
			msgs = append(msgs, um)
		case role == string(schema.Assistant) || role == "assistant":
			var tcs []schema.ToolCall
			if len(m.ToolCalls) > 0 {
				tcs = m.ToolCalls
			}
			msgs = append(msgs, schema.AssistantMessage(m.Content, tcs))
		case role == string(schema.Tool) || role == "tool":
			msgs = append(msgs, schema.ToolMessage(m.Content, m.ToolCallID))
		default:
			msgs = append(msgs, schema.UserMessage(m.Content))
		}
	}

	if len(p.ToolOutputs) > 0 {
		var b strings.Builder
		b.WriteString(tagEnvFeedback)
		b.WriteString("\n")
		for _, t := range p.ToolOutputs {
			b.WriteString(t)
			b.WriteString("\n")
		}
		msgs = append(msgs, schema.UserMessage(strings.TrimSpace(b.String())))
	}

	if len(msgs) == 0 {
		return []*schema.Message{schema.UserMessage("（无上下文）")}, nil
	}
	return msgs, nil
}

// FormatWorkflowContext returns a tagged line for injection before spine compile.
func FormatWorkflowContext(line string) string {
	return fmt.Sprintf("%s\n%s", tagWorkflowContext, strings.TrimSpace(line))
}
