package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/crushproto"
	"agentgo/internal/engine"
)

// crushRunner runs a Crush TUI prompt through AgentGo's engine.Router, so the
// stock Crush TUI is a "body" of the AgentGo brain: Eino agent, governance,
// memory and plugin tools all apply; the TUI only renders.
type crushRunner struct {
	svc      *AppService
	engineID string // "" = Router default
}

func newCrushRunner(svc *AppService, engineID string) *crushRunner {
	return &crushRunner{svc: svc, engineID: engineID}
}

// agentSession returns the AgentGo session behind a Crush session, creating it
// on first use so the conversation also shows up in the desktop session list.
// The link is persisted by crushproto, so it survives a TUI/process restart.
func (r *crushRunner) agentSession(em *crushproto.Emitter, crushID, prompt string) string {
	if id, ok := em.Link(); ok {
		return id
	}
	id := crushID // fallback: history is keyed by the crush id
	if out := r.svc.NewSession(titleFrom(prompt)); out["success"] == true {
		if s, _ := out["id"].(string); s != "" {
			id = s
		}
	}
	em.SetLink(id)
	return id
}
func titleFrom(prompt string) string {
	r := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(r) > 40 {
		r = append(r[:40], '…')
	}
	if len(r) == 0 {
		return "Crush session"
	}
	return string(r)
}

func (r *crushRunner) Run(ctx context.Context, req crushproto.RunRequest, em *crushproto.Emitter) error {
	em.UserMessage(req.Prompt)
	t := newCrushTurn(em)

	agentSID := r.agentSession(em, req.SessionID, req.Prompt)
	ctx = agent.WithUsageObserver(ctx, t)
	ctx = agent.WithHistory(ctx, r.svc.sessionHistory(ctx, agentSID)) // before runEngineTurn appends this prompt
	res := r.svc.runEngineTurn(ctx, engine.RunRequest{
		Engine:    r.engineID,
		SessionID: agentSID,
		Input:     req.Prompt,
		Workspace: em.WorkDir(),
	}, engine.SinkFunc(t.onEvent))

	if os.Getenv("AGENTGO_DEBUG_LLM") == "1" {
		shapes := make([]string, 0, len(res.Messages))
		for _, m := range res.Messages {
			shapes = append(shapes, fmt.Sprintf("%s/%s/%s/%s:%.100q", m.Role, m.Type, m.Status, m.ToolName, m.Content))
		}
		agent.DebugLogf("crush run done: ctxErr=%v error=%q messages=[%s]", ctx.Err(), res.Error, strings.Join(shapes, " "))
	}
	if ctx.Err() != nil { // cancelled by the TUI
		t.finish()
		return ctx.Err()
	}

	// Non-streamed paths (tool-less fallback, resumed runs) only have the final text.
	if !t.sawText() {
		for _, m := range res.Messages {
			if m.Role == "assistant" && (m.Type == "text" || m.Type == "") {
				t.text(m.Content)
			}
		}
	}
	if res.Error != "" {
		t.text("Error: " + res.Error)
	}
	for _, m := range res.Messages {
		if m.Status != "pending" {
			continue
		}
		switch m.Type {
		case "approval":
			r.approve(ctx, em, t, m)
		case "question":
			// ask_user needs a free-text reply; Crush has no such UI. Show it,
			// the user answers with their next prompt.
			t.text(m.Content)
		}
	}

	t.finish()
	prompt, completion := t.usage()
	// Complete only applies the title while the session still has a placeholder one.
	em.Complete(req, t.lastID, t.lastText, titleFrom(req.Prompt), prompt, completion)
	return nil
}

// approve maps AgentGo's interrupt-then-resume approval to Crush's blocking
// permission dialog: ask the TUI, then resolve and (if granted) resume.
func (r *crushRunner) approve(ctx context.Context, em *crushproto.Emitter, t *crushTurn, m engine.Message) {
	callID := t.toolCall("approval_"+m.ApprovalID, m.ToolName, m.Arguments)

	var params any = map[string]any{}
	if m.Arguments != "" {
		var p any
		if json.Unmarshal([]byte(m.Arguments), &p) == nil {
			params = p
		}
	}
	// The desktop UI (or any other surface of this host) may decide first: then
	// the dialog closes and the outcome comes from the relay instead of from here.
	watch := r.svc.rt.relay.watch(m.ApprovalID)
	other, stopWatch := watch.Decision(ctx)
	defer stopWatch()
	granted, byOther := em.AwaitPermission(crushproto.PermissionRequest{
		ToolCallID:  callID,
		ToolName:    m.ToolName,
		Description: m.Content,
		Action:      m.ToolName,
		Params:      params,
		Path:        em.WorkDir(),
	}, other)
	if ctx.Err() != nil {
		return
	}

	var out map[string]any
	if !byOther {
		// ctx is the TUI run's context: cancelling the run cancels the resume too.
		out = r.svc.resolveApproval(ctx, m.ApprovalID, granted, "crush-tui", "desktop_user", "")
		agent.DebugLogf("resolve approval granted=%v success=%v error=%v resume_error=%v", granted, out["success"], out["error"], out["resume_error"])
		if ok, _ := out["success"].(bool); !ok && watch.Decided(2*time.Second) {
			byOther = true // lost a race with another surface: its decision stands
		}
	}
	if byOther {
		var ok bool
		if granted, out, ok = watch.Outcome(ctx); !ok {
			return
		}
		if by := watch.DecidedBy(); by != "" {
			t.text(fmt.Sprintf("（该审批已由 %s 处理）", by))
		}
	}
	r.showOutcome(ctx, em, t, callID, m, granted, out)
}

// showOutcome renders what a decision led to, whoever took it: the tool result,
// the resumed run's text, and any further approval the resumed run stopped for.
func (r *crushRunner) showOutcome(ctx context.Context, em *crushproto.Emitter, t *crushTurn, callID string, m engine.Message, granted bool, out map[string]any) {
	if ok, _ := out["success"].(bool); !ok {
		e, _ := out["error"].(string)
		t.toolResult(callID, m.ToolName, "Approval could not be recorded: "+e, true)
		t.text("Error: " + e)
		return
	}
	if !granted {
		t.toolResult(callID, m.ToolName, "User denied permission", true)
		return
	}
	if e, _ := out["resume_error"].(string); e != "" {
		t.toolResult(callID, m.ToolName, "Approved, but the run could not continue: "+e, true)
		t.text("Error: " + e)
		return
	}
	t.toolResult(callID, m.ToolName, "Approved and executed.", false)

	// The resumed run may stop again for another approval; ask for each in turn.
	msgs, _ := out["messages"].([]ChatMessageDTO)
	for _, dto := range msgs {
		if dto.Role != "assistant" {
			continue
		}
		switch {
		case dto.Type == "approval" && dto.Status == "pending":
			r.approve(ctx, em, t, engine.Message{
				Role: dto.Role, Type: dto.Type, Content: dto.Content, ApprovalID: dto.ApprovalID,
				ToolName: dto.ToolName, Arguments: dto.Arguments, Status: dto.Status,
			})
		default:
			t.text(dto.Content)
		}
	}
}
