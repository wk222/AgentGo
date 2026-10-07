package crushproto

import (
	"context"
	"strings"
)

// Runner executes one agent turn and narrates it through the Emitter. The
// probe uses a scripted Runner; the production one bridges to AgentGo.
type Runner interface {
	Run(ctx context.Context, req RunRequest, em *Emitter) error
}

// Emitter translates "what the agent is doing" into Crush's message-snapshot
// events. It is used by exactly one run goroutine.
type Emitter struct {
	ctx       context.Context
	ws        *workspace
	sessionID string
	model     string
	provider  string
}

// WorkDir is the workspace directory the client registered.
func (e *Emitter) WorkDir() string { return e.ws.path }

// UserMessage records the user's prompt.
func (e *Emitter) UserMessage(text string) {
	now := nowSec()
	m := &Message{
		Parts: []Part{
			{Type: "text", Data: map[string]any{"text": text}},
			{Type: "finish", Data: map[string]any{"reason": "stop", "time": 0}},
		},
		ID: newID(), Role: "user", SessionID: e.sessionID, CreatedAt: now, UpdatedAt: now,
	}
	e.ws.putMessage(m, "created")
	e.bumpCount(1)
}

// Assistant starts a new assistant message (empty parts, as Crush does).
func (e *Emitter) Assistant() *AssistantMsg {
	now := nowSec()
	a := &AssistantMsg{e: e, msg: Message{
		Parts: []Part{}, ID: newID(), Role: "assistant", SessionID: e.sessionID,
		Model: e.model, Provider: e.provider, CreatedAt: now, UpdatedAt: now,
	}}
	e.ws.putMessage(&a.msg, "created")
	e.bumpCount(1)
	return a
}

// ToolResult records a tool's output as a separate role=tool message.
func (e *Emitter) ToolResult(callID, name, content, metadata string, isErr bool) {
	now := nowSec()
	m := &Message{
		Parts: []Part{
			{Type: "tool_result", Data: map[string]any{
				"tool_call_id": callID, "name": name, "content": content,
				"metadata": metadata, "is_error": isErr,
			}},
			{Type: "finish", Data: map[string]any{"reason": "stop", "time": 0}},
		},
		ID: newID(), Role: "tool", SessionID: e.sessionID, Model: e.model, Provider: e.provider,
		CreatedAt: now, UpdatedAt: now,
	}
	e.ws.putMessage(m, "created")
	e.bumpCount(1)
}

// RequestPermission publishes a permission request and blocks until a client
// grants or denies it (or the run is cancelled). Returns true when granted.
func (e *Emitter) RequestPermission(req PermissionRequest) bool {
	granted, _ := e.AwaitPermission(req, nil)
	return granted
}

// AwaitPermission is RequestPermission for a decision that another surface may
// take first (the desktop UI approving a TUI-started run). A value on other
// decides the request: the client's dialog is closed exactly as for its own
// answer, and byOther reports that the decision did not come from this client.
//
// While the request waits it stays in the workspace's outstanding set, so a
// client that reconnects is shown it again (see subscribe).
func (e *Emitter) AwaitPermission(req PermissionRequest, other <-chan bool) (granted, byOther bool) {
	if req.ID == "" {
		req.ID = newID()
	}
	req.SessionID = e.sessionID
	ch := make(chan bool, 1)
	e.ws.mu.Lock()
	if e.ws.skipAll {
		e.ws.mu.Unlock()
		return true, false
	}
	e.ws.pending[req.ID] = ch
	// Recorded and announced under one lock: a subscriber that attaches now sees
	// the request either in its replay or on the live stream, never both.
	e.ws.asked[req.ID] = req
	if b, err := envelope("permission_notification", "created",
		map[string]any{"tool_call_id": req.ToolCallID, "granted": false, "denied": false}); err == nil {
		e.ws.publishLocked(b)
	}
	if b, err := envelope("permission_request", "created", req); err == nil {
		e.ws.publishLocked(b)
	}
	e.ws.mu.Unlock()

	select {
	case granted = <-ch:
	case granted = <-other:
		byOther = true
	case <-e.ctx.Done():
	}
	e.ws.mu.Lock()
	delete(e.ws.pending, req.ID)
	delete(e.ws.asked, req.ID)
	e.ws.mu.Unlock()
	e.ws.publish("permission_notification", "created",
		map[string]any{"tool_call_id": req.ToolCallID, "granted": granted, "denied": !granted})
	return granted, byOther
}

// Complete closes the turn: title (first turn), token usage, agent_finished and
// run_complete — the event `crush run` and the TUI wait for.
func (e *Emitter) Complete(req RunRequest, finalMessageID, finalText, title string, promptTok, complTok int) {
	e.ws.updateSession(e.sessionID, func(s *Session) {
		if title != "" && (s.Title == "" || s.Title == "non-interactive" || s.Title == "New Session") {
			s.Title = title
		}
		// Latest call wins (context size), as in Crush; 0/0 keeps the old value.
		if promptTok > 0 || complTok > 0 {
			s.PromptTokens, s.CompletionTokens = promptTok, complTok
		}
	})
	sess, _ := e.ws.session(e.sessionID)
	e.ws.publish("agent_event", "created", map[string]any{
		"type": "agent_finished", "session_id": e.sessionID, "session_title": sess.Title,
		"message": Message{Parts: []Part{}},
	})
	e.ws.publish("run_complete", "updated", map[string]any{
		"session_id": e.sessionID, "run_id": req.RunID, "message_id": finalMessageID, "text": finalText,
	})
}

func (e *Emitter) bumpCount(n int) {
	e.ws.updateSession(e.sessionID, func(s *Session) { s.MessageCount += n })
}

// AssistantMsg builds one assistant message incrementally; each change
// publishes a full snapshot.
type AssistantMsg struct {
	e   *Emitter
	msg Message
}

// ID of the message (for run_complete).
func (a *AssistantMsg) ID() string { return a.msg.ID }

func (a *AssistantMsg) push() {
	a.msg.UpdatedAt = nowSec()
	a.e.ws.putMessage(&a.msg, "updated")
}

func (a *AssistantMsg) find(typ, key, val string) *Part {
	for i := range a.msg.Parts {
		p := &a.msg.Parts[i]
		if p.Type == typ && (key == "" || p.Data[key] == val) {
			return p
		}
	}
	return nil
}

// ReasoningStart/ReasoningDone bracket a reasoning part.
func (a *AssistantMsg) ReasoningStart() {
	if a.find("reasoning", "", "") == nil {
		a.msg.Parts = append(a.msg.Parts, Part{Type: "reasoning", Data: map[string]any{
			"thinking": "", "signature": "", "started_at": nowSec()}})
		a.push()
	}
}

// AppendReasoning streams reasoning/thinking text into a reasoning part.
func (a *AssistantMsg) AppendReasoning(delta string) {
	p := a.find("reasoning", "", "")
	if p == nil {
		a.msg.Parts = append(a.msg.Parts, Part{Type: "reasoning", Data: map[string]any{
			"thinking": "", "signature": "", "started_at": nowSec()}})
		p = &a.msg.Parts[len(a.msg.Parts)-1]
	}
	p.Data["thinking"] = p.Data["thinking"].(string) + delta
	a.push()
}

func (a *AssistantMsg) ReasoningDone() {
	if p := a.find("reasoning", "", ""); p != nil {
		if fa, ok := p.Data["finished_at"].(int64); !ok || fa == 0 {
			p.Data["finished_at"] = nowSec()
			a.push()
		}
	}
}

// ToolCallStart announces a call before its arguments are known (input "").
func (a *AssistantMsg) ToolCallStart(id, name string) {
	a.msg.Parts = append(a.msg.Parts, Part{Type: "tool_call", Data: map[string]any{
		"id": id, "name": name, "input": ""}})
	a.push()
}

// ToolCallInput completes the call's arguments.
func (a *AssistantMsg) ToolCallInput(id, inputJSON string) {
	if p := a.find("tool_call", "id", id); p != nil {
		p.Data["input"] = inputJSON
		p.Data["finished"] = true
		a.push()
	}
}

// AppendText streams assistant text into a single text part.
func (a *AssistantMsg) AppendText(delta string) {
	p := a.find("text", "", "")
	if p == nil {
		a.msg.Parts = append(a.msg.Parts, Part{Type: "text", Data: map[string]any{"text": ""}})
		p = &a.msg.Parts[len(a.msg.Parts)-1]
	}
	p.Data["text"] = p.Data["text"].(string) + delta
	a.push()
}

// Text returns the accumulated text.
func (a *AssistantMsg) Text() string {
	if p := a.find("text", "", ""); p != nil {
		return strings.TrimSpace(p.Data["text"].(string))
	}
	return ""
}

// Finish ends the message: reason is "tool_use" or "end_turn".
func (a *AssistantMsg) Finish(reason string) {
	a.msg.Parts = append(a.msg.Parts, Part{Type: "finish", Data: map[string]any{
		"reason": reason, "time": nowSec()}})
	a.push()
}
