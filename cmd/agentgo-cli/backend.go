package main

import (
	"fmt"
	"os"
	"strings"

	"agentgo/internal/applog"
	"agentgo/internal/bridge"
	"agentgo/internal/cli"
	"agentgo/internal/config"
	"agentgo/internal/engine"
)

// bridgeBackend runs the brain inside this process.
type bridgeBackend struct {
	rt  *bridge.Runtime
	svc *bridge.AppService
}

// openStandalone assembles the AgentGo runtime in this process, bound to the
// workspace in cfg. LLM settings come from AgentGo's saved configuration; when
// AZURE_OPENAI_API_KEY and AZURE_OPENAI_API_ENDPOINT are set they take
// precedence for this process.
func openStandalone(cfg cli.Config) (cli.Backend, error) {
	root := strings.TrimSpace(cfg.Workspace) // empty = keep the workspace AgentGo last used
	if cfg.API != "" {
		config.LLMAPI.Set(cfg.API)
	}
	// A CLI process must not fire the desktop app's scheduled tasks.
	config.DisableScheduler.SetDefault(true)

	applog.SetQuiet(!cfg.Verbose)
	rt, err := bridge.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	if root != "" {
		if _, err := rt.SetWorkspaceRoot(root); err != nil {
			if strings.Contains(err.Error(), "waiting for approval") {
				return nil, fmt.Errorf("workspace %s: %w\n(list them with `agentgo-cli approvals`, settle with `agentgo-cli approve|deny ID`)", root, err)
			}
			return nil, fmt.Errorf("workspace %s: %w", root, err)
		}
	}
	if key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT"); key != "" && ep != "" {
		model := config.AzureDeployment.Get()
		if model == "" {
			model = "gpt-6-luna"
		}
		if !config.ReasoningEffort.IsSet() {
			// chat/completions rejects function tools on reasoning models unless
			// effort is "none"; the Responses API has no such limit.
			if strings.EqualFold(config.LLMAPI.Get(), "responses") {
				config.ReasoningEffort.Set("medium")
			} else {
				config.ReasoningEffort.Set("none")
			}
		}
		llm := bridge.LLMConfig{APIBase: strings.TrimRight(ep, "/") + "/openai/v1/", APIKey: key, Model: model}
		if err := rt.SetLLMConfig(llm); err != nil {
			return nil, fmt.Errorf("llm config: %w", err)
		}
	}
	return &bridgeBackend{rt: rt, svc: bridge.NewAppService(rt)}, nil
}

func (b *bridgeBackend) Run(sessionID, input string, sink engine.EventSink) engine.RunResult {
	return b.svc.RunEngineSync("", sessionID, input, nil, sink)
}

func (b *bridgeBackend) Approve(approvalID string, approved bool) cli.Result {
	return b.ApproveStream(approvalID, approved, nil)
}

// ApproveStream is Approve with the continued run's events delivered to sink
// (tui.Streamer), so the screen shows the tool the approval unblocked.
func (b *bridgeBackend) ApproveStream(approvalID string, approved bool, sink engine.EventSink) cli.Result {
	for _, q := range b.svc.ListPendingQuestions() {
		if q.ID != approvalID {
			continue
		}
		// A question is not approved or denied: it is answered (in a chat on its
		// session) or dropped.
		if approved {
			return cli.Result{Error: "this is a question, not an approval; answer it in the chat: agentgo-cli -session " + q.SessionID}
		}
		if !b.svc.DiscardPendingQuestion(approvalID) {
			return cli.Result{Error: "the question was already settled"}
		}
		return cli.Result{}
	}
	out := b.svc.ResolveApprovalTap(approvalID, approved, "cli", sink)
	var r cli.Result
	if ok, _ := out["success"].(bool); !ok {
		r.Error = str(out["error"])
		if r.Error == "" {
			r.Error = "the decision was not recorded"
		}
		return r
	}
	if e := str(out["resume_error"]); e != "" {
		r.Error = e
	}
	if msgs, ok := out["messages"].([]bridge.ChatMessageDTO); ok {
		for _, m := range msgs {
			r.Messages = append(r.Messages, fromDTO(m))
		}
	}
	return r
}

func (b *bridgeBackend) Answer(questionID, text string) cli.Result {
	return b.AnswerStream(questionID, text, nil)
}

// AnswerStream is Answer with the continued run's events delivered to sink.
func (b *bridgeBackend) AnswerStream(questionID, text string, sink engine.EventSink) cli.Result {
	out := b.svc.AnswerQuestionTap(questionID, text, sink)
	var r cli.Result
	if ok, _ := out["success"].(bool); !ok {
		r.Error = str(out["error"])
		return r
	}
	if c := str(out["content"]); c != "" {
		r.Messages = append(r.Messages, cli.Message{Role: "assistant", Type: "text", Content: c})
	}
	if m, ok := out["message"].(bridge.ChatMessageDTO); ok {
		r.Messages = append(r.Messages, fromDTO(m))
	}
	return r
}

func (b *bridgeBackend) Cancel(sessionID string) { b.svc.CancelEngineSession(sessionID) }

func (b *bridgeBackend) NewSession(title string) (string, error) {
	out := b.svc.NewSession(title)
	if ok, _ := out["success"].(bool); !ok {
		return "", fmt.Errorf("new session: %s", str(out["error"]))
	}
	return str(out["id"]), nil
}

func (b *bridgeBackend) Sessions() ([]cli.SessionInfo, error) {
	var out []cli.SessionInfo
	for _, s := range b.svc.ListSessions() {
		info := cli.SessionInfo{ID: str(s["id"]), Title: str(s["title"])}
		info.UpdatedAt = toInt64(s["updated_at"])
		info.Messages = int(toInt64(s["message_count"]))
		out = append(out, info)
	}
	return out, nil
}

func (b *bridgeBackend) Plugins() []cli.PluginInfo {
	var out []cli.PluginInfo
	for _, p := range b.svc.ListPlugins() {
		out = append(out, cli.PluginInfo{Name: p.Name, State: string(p.State), Error: p.Error})
	}
	return out
}

func (b *bridgeBackend) Pending() []cli.Message {
	var out []cli.Message
	for _, a := range b.svc.ListPendingApprovals() {
		out = append(out, cli.Message{Role: "assistant", Type: "approval", ApprovalID: a.ID,
			ToolName: a.ToolName, Arguments: a.Arguments, Content: a.Summary, Status: "pending"})
	}
	// Unanswered ask_user pauses block a workspace switch just like approvals.
	for _, q := range b.svc.ListPendingQuestions() {
		out = append(out, cli.Message{Role: "assistant", Type: "question", ApprovalID: q.ID,
			ToolName: "ask_user", Arguments: "session " + q.SessionID, Content: q.Prompt, Status: "pending"})
	}
	return out
}

func (b *bridgeBackend) Tools() []string { return b.svc.ListRegisteredTools() }

// History replays a saved conversation for the TUI (tui.Historian): the user
// and assistant text, in order.
func (b *bridgeBackend) History(sessionID string) ([]cli.Message, error) {
	out := b.svc.GetSessionMessages(sessionID)
	if e := str(out["error"]); e != "" {
		return nil, fmt.Errorf("%s", e)
	}
	rows, _ := out["messages"].([]map[string]any)
	var msgs []cli.Message
	for _, r := range rows {
		msgs = append(msgs, cli.Message{Role: str(r["role"]), Type: str(r["type"]), Content: str(r["content"])})
	}
	return msgs, nil
}

func (b *bridgeBackend) Close() {
	b.svc.Close()
	_ = b.rt.Close()
}

func fromDTO(m bridge.ChatMessageDTO) cli.Message {
	return cli.Message{
		Role: m.Role, Type: m.Type, Content: m.Content,
		ApprovalID: m.ApprovalID, ToolName: m.ToolName, Arguments: m.Arguments, Status: m.Status,
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
