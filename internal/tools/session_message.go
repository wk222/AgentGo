package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
)

// SessionMessageHandler is implemented by the host, which owns the conversations.
type SessionMessageHandler interface {
	ListSessions(ctx context.Context, selfID, query string, limit int) (any, error)
	SendSessionMessage(ctx context.Context, fromID, toID, text string) (any, error)
	ReadSession(ctx context.Context, selfID, targetID string, limit int) (any, error)
}

var (
	sessionMsgMu      sync.RWMutex
	sessionMsgHandler SessionMessageHandler
)

// SetSessionMessageHandler wires the host's conversations into the session_message tool.
func SetSessionMessageHandler(h SessionMessageHandler) {
	sessionMsgMu.Lock()
	sessionMsgHandler = h
	sessionMsgMu.Unlock()
}

// MaxSessionMessageChars bounds one message; a longer text belongs in a file the other side can read.
const MaxSessionMessageChars = 8000

type sessionMessageInput struct {
	Action string `json:"action" jsonschema:"description=Action: 'list' (recent conversations)\\, 'send' (message another conversation) or 'read' (its latest messages)"`
	To     string `json:"to,omitempty" jsonschema:"description=Target conversation id (send/read)\\, from 'list'"`
	Text   string `json:"text,omitempty" jsonschema:"description=The message (send). Write it self-contained: the other conversation sees only this text and who it came from"`
	Query  string `json:"query,omitempty" jsonschema:"description=list: keep only conversations whose title contains this"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=list: how many conversations (default 15\\, max 50); read: how many messages (default 10\\, max 30)"`
}

type sessionMessageOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Result  any    `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterSessionMessageTool registers `session_message`: conversations can talk to each other.
// A message waits until the target is idle, then starts a turn there; it never cuts into one.
func RegisterSessionMessageTool(r *Registry) error {
	t, err := utils.InferTool("session_message",
		"Talk to ANOTHER conversation of this user (another agent working on a different task or project). "+
			"'list' shows recent conversations; 'read' shows the latest messages of one; 'send' delivers a message that starts a turn there once it is idle, "+
			"tagged with who sent it so it can answer back with the same tool. "+
			"Use it to hand work over, ask what another conversation found, or report a result to the conversation that asked. "+
			"Do not use it to talk to yourself or to ping repeatedly: deliveries are rate-limited.",
		func(ctx context.Context, in sessionMessageInput) (sessionMessageOutput, error) {
			sessionMsgMu.RLock()
			h := sessionMsgHandler
			sessionMsgMu.RUnlock()
			action := strings.ToLower(strings.TrimSpace(in.Action))
			if h == nil {
				return sessionMessageOutput{Action: action, Error: "cross-conversation messaging is not available in this host"}, nil
			}
			self := governance.SessionIDFromContext(ctx)
			fail := func(err error) (sessionMessageOutput, error) {
				return sessionMessageOutput{Action: action, Error: err.Error()}, nil
			}
			to := strings.TrimSpace(in.To)
			switch action {
			case "list":
				res, err := h.ListSessions(ctx, self, strings.TrimSpace(in.Query), clampLimit(in.Limit, 15, 50))
				if err != nil {
					return fail(err)
				}
				return sessionMessageOutput{Success: true, Action: action, Result: res}, nil
			case "read":
				if to == "" {
					return fail(fmt.Errorf("to is required"))
				}
				res, err := h.ReadSession(ctx, self, to, clampLimit(in.Limit, 10, 30))
				if err != nil {
					return fail(err)
				}
				return sessionMessageOutput{Success: true, Action: action, Result: res}, nil
			case "send":
				text := strings.TrimSpace(in.Text)
				if to == "" || text == "" {
					return fail(fmt.Errorf("to and text are required"))
				}
				if len([]rune(text)) > MaxSessionMessageChars {
					return fail(fmt.Errorf("text is too long (max %d characters); put it in a file and send the path", MaxSessionMessageChars))
				}
				res, err := h.SendSessionMessage(ctx, self, to, text)
				if err != nil {
					return fail(err)
				}
				return sessionMessageOutput{Success: true, Action: action, Result: res,
					Message: "已送达。对方会话空闲后会开始处理，不需要在这里等待。"}, nil
			default:
				return fail(fmt.Errorf("unknown action %q, use list/send/read", in.Action))
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func clampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	return min(v, max)
}
