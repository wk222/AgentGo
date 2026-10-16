package core

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/agent"
	"agentgo/internal/sessions"
)

// Log is the persistence a chat turn needs; *sessions.Store satisfies it.
type Log interface {
	EnsureSession(ctx context.Context, id string) error
	GetMessages(ctx context.Context, sessionID string, limit int) ([]sessions.Message, error)
	AppendMessage(ctx context.Context, sessionID, role, content, msgType string, meta map[string]any) error
	AutoTitleFromUserMessage(ctx context.Context, sessionID, content string) error
}

// Message is one reply message of a finished turn, in persistable form.
type Message struct {
	Role    string
	Content string
	Type    string // "" or "text" for prose; "approval" / "question" carry the fields below
	// approval / question fields
	ApprovalID string
	ToolName   string
	Arguments  string
	Status     string
}

func (m Message) meta() map[string]any {
	meta := map[string]any{}
	if m.Type == "approval" || m.Type == "question" {
		meta["approval_id"] = m.ApprovalID
		meta["tool_name"] = m.ToolName
		meta["arguments"] = m.Arguments
		meta["status"] = m.Status
	}
	return meta
}

// Transcript owns the conversation record of a session: it loads the history a
// run starts from and stores what the user and the agent said. Every entry
// point must go through it, otherwise a session forgets its earlier turns.
type Transcript struct{ log Log }

// NewTranscript returns a Transcript over log. A nil log disables persistence.
func NewTranscript(log Log) *Transcript { return &Transcript{log: log} }

// History returns the replayable history of a session (empty when unknown).
func (t *Transcript) History(ctx context.Context, sessionID string) []*schema.Message {
	if t == nil || t.log == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	msgs, err := t.log.GetMessages(ctx, sessionID, 500)
	if err != nil {
		return nil
	}
	return BuildHistory(msgs, HistoryMaxMessages, HistoryMaxRunes)
}

// Begin prepares a turn: it makes sure the session exists, loads the history
// into the returned context, then stores the user message. The history is read
// first, so it never contains the message of the turn being started.
func (t *Transcript) Begin(ctx context.Context, sessionID, userText string, images []string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if t == nil || t.log == nil || sessionID == "" {
		return ctx
	}
	_ = t.log.EnsureSession(context.Background(), sessionID)
	ctx = agent.WithHistory(ctx, t.History(ctx, sessionID))
	var meta map[string]any
	if len(images) > 0 {
		meta = map[string]any{"images": images}
	}
	pctx := context.Background() // the record must survive cancellation of the run
	_ = t.log.AppendMessage(pctx, sessionID, "user", userText, "text", meta)
	_ = t.log.AutoTitleFromUserMessage(pctx, sessionID, userText)
	return ctx
}

// Finish stores the reply messages of a turn.
func (t *Transcript) Finish(sessionID string, msgs []Message) {
	sessionID = strings.TrimSpace(sessionID)
	if t == nil || t.log == nil || sessionID == "" {
		return
	}
	pctx := context.Background()
	for _, m := range msgs {
		typ := m.Type
		if typ == "" {
			typ = "text"
		}
		_ = t.log.AppendMessage(pctx, sessionID, m.Role, m.Content, typ, m.meta())
	}
}
