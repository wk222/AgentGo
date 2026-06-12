package sessions

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// CompileOptions configures message → ProjectedRuntimeView classification.
type CompileOptions struct {
	MaxActiveTurns int
	DurableTasks   []string
	WorkspaceRules string // SOUL/TEAM/rules preloaded (optional)
	Snapshot       SpineSnapshot
}

const (
	tagSystemSummary   = "[System Summary]"
	tagMemoryInject    = "[Injected Memory Context]"
	tagWorkspace       = "[Workspace Context]"
	tagEnvFeedback     = "[Environment Feedback]"
	tagWorkflowContext = "[Workflow Context]"
	tagAppMatrix       = "[App Matrix]"
	tagContextHygiene  = "[Context Hygiene]"
	tagTeamMemory      = "[Team Memory]"
	tagIsolation       = "[Isolation]"
	tagModeProfile     = "[Mode Profile]"
)

// CompileProjectedView classifies flat ADK messages into the six-section runtime projection.
func CompileProjectedView(messages []*schema.Message, opt CompileOptions) *ProjectedRuntimeView {
	if opt.MaxActiveTurns <= 0 {
		opt.MaxActiveTurns = 30
	}
	view := &ProjectedRuntimeView{
		DurableTasks:    append([]string(nil), opt.DurableTasks...),
		WorkflowContext: append([]string(nil), opt.Snapshot.WorkflowContext...),
		EpisodicSummary: append([]string(nil), opt.Snapshot.EpisodicSummary...),
		ContextHygiene:  append([]string(nil), opt.Snapshot.ContextHygiene...),
		TeamMemory:      append([]string(nil), opt.Snapshot.TeamMemory...),
		Isolation:       append([]string(nil), opt.Snapshot.Isolation...),
	}
	if rules := strings.TrimSpace(opt.WorkspaceRules); rules != "" {
		view.SystemRules = append(view.SystemRules, rules)
	}
	if len(opt.Snapshot.WorkspaceRecent) > 0 {
		view.WorkspaceRecent = append([]string(nil), opt.Snapshot.WorkspaceRecent...)
	}

	var active []FlatMessage
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" && len(msg.ToolCalls) == 0 {
			continue
		}

		switch {
		case msg.Role == schema.System:
			view.SystemRules = append(view.SystemRules, classifySystemContent(content)...)

		case msg.Role == schema.User:
			switch {
			case strings.HasPrefix(content, tagSystemSummary):
				view.EpisodicSummary = append(view.EpisodicSummary, strings.TrimSpace(strings.TrimPrefix(content, tagSystemSummary)))
			case strings.HasPrefix(content, tagMemoryInject):
				view.EpisodicSummary = append(view.EpisodicSummary, strings.TrimSpace(strings.TrimPrefix(content, tagMemoryInject)))
			case strings.HasPrefix(content, tagEnvFeedback):
				view.ToolOutputs = append(view.ToolOutputs, strings.TrimSpace(strings.TrimPrefix(content, tagEnvFeedback)))
			case strings.HasPrefix(content, tagWorkflowContext), strings.HasPrefix(content, tagAppMatrix):
				view.WorkflowContext = append(view.WorkflowContext, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagContextHygiene):
				view.ContextHygiene = append(view.ContextHygiene, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagTeamMemory):
				view.TeamMemory = append(view.TeamMemory, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagIsolation), strings.HasPrefix(content, tagModeProfile):
				view.Isolation = append(view.Isolation, stripKnownPrefix(content))
			default:
				active = append(active, flatFromSchema(msg))
			}

		case msg.Role == schema.Assistant:
			active = append(active, flatFromSchema(msg))

		case msg.Role == schema.Tool:
			fm := flatFromSchema(msg)
			fm.Role = "tool"
			fm.ToolCallID = msg.ToolCallID
			active = append(active, fm)
		}
	}

	dropped := 0
	if len(active) > opt.MaxActiveTurns {
		dropped = len(active) - opt.MaxActiveTurns
		active = active[len(active)-opt.MaxActiveTurns:]
	}
	if dropped > 0 {
		view.ContextHygiene = append(view.ContextHygiene,
			fmt.Sprintf("活跃窗口保留最近 %d 条消息；另有 %d 条较早轮次未送入本轮模型（请依赖 episodic_summary）。", opt.MaxActiveTurns, dropped))
	}
	view.ActiveTurn = active
	return view
}

func flatFromSchema(msg *schema.Message) FlatMessage {
	fm := FlatMessage{
		Role:    string(msg.Role),
		Content: strings.TrimSpace(msg.Content),
	}
	if len(msg.ToolCalls) > 0 {
		fm.ToolCalls = append([]schema.ToolCall(nil), msg.ToolCalls...)
	}
	if msg.Role == schema.Tool {
		fm.ToolCallID = msg.ToolCallID
	}
	if len(msg.UserInputMultiContent) > 0 {
		fm.UserInputMultiContent = append([]schema.MessageInputPart(nil), msg.UserInputMultiContent...)
	}
	return fm
}

func classifySystemContent(content string) []string {
	c := strings.TrimSpace(content)
	if strings.HasPrefix(c, tagWorkspace) {
		return []string{strings.TrimSpace(strings.TrimPrefix(c, tagWorkspace))}
	}
	if strings.HasPrefix(c, tagMemoryInject) {
		// memory sometimes lands in system merge from other MW — treat as rules + summary split
		body := strings.TrimSpace(strings.TrimPrefix(c, tagMemoryInject))
		return []string{body}
	}
	return []string{c}
}

func stripKnownPrefix(content string) string {
	for _, p := range []string{tagWorkflowContext, tagAppMatrix, tagContextHygiene, tagTeamMemory, tagIsolation, tagModeProfile} {
		if strings.HasPrefix(content, p) {
			return strings.TrimSpace(strings.TrimPrefix(content, p))
		}
	}
	return content
}
