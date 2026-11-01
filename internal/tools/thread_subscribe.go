package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
)

// ThreadHandler is implemented by the host, which owns the thread bindings.
type ThreadHandler interface {
	SubscribeThread(ctx context.Context, sessionID, key, title string) (any, error)
	UnsubscribeThread(ctx context.Context, sessionID, key string) error
	ListThreads(ctx context.Context, sessionID string) (any, error)
}

var (
	threadMu      sync.RWMutex
	threadHandler ThreadHandler
)

// SetThreadHandler wires the host's thread bindings into the thread_subscribe tool.
func SetThreadHandler(h ThreadHandler) {
	threadMu.Lock()
	threadHandler = h
	threadMu.Unlock()
}

type threadInput struct {
	Action string `json:"action" jsonschema:"description=Action: 'subscribe'\\, 'unsubscribe' or 'list'"`
	Key    string `json:"key,omitempty" jsonschema:"description=Thread key that outside systems put on their events\\, e.g. 'github:acme/app#12' (a pull request) or 'mail:<message-id>'"`
	Title  string `json:"title,omitempty" jsonschema:"description=Short label for the thread"`
}

type threadOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Result  any    `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterThreadSubscribeTool registers `thread_subscribe`: bind an outside
// thread (a pull request, an email thread, a chat room) to this conversation,
// so every later event on it arrives here with the history already in place.
func RegisterThreadSubscribeTool(r *Registry) error {
	t, err := utils.InferTool("thread_subscribe",
		"Follow an outside thread from this conversation. After you subscribe, every new event on that thread key "+
			"(a PR comment, an email reply, a CI result) is delivered to THIS conversation as a new turn, so you keep the full context "+
			"instead of starting over. Use when you have opened or taken over something that others will reply to. "+
			"A binding stays until you unsubscribe.",
		func(ctx context.Context, in threadInput) (threadOutput, error) {
			threadMu.RLock()
			h := threadHandler
			threadMu.RUnlock()
			if h == nil {
				return threadOutput{Action: in.Action, Error: "thread subscriptions are not available in this host"}, nil
			}
			sid := governance.SessionIDFromContext(ctx)
			switch strings.ToLower(strings.TrimSpace(in.Action)) {
			case "subscribe":
				res, err := h.SubscribeThread(ctx, sid, in.Key, strings.TrimSpace(in.Title))
				if err != nil {
					return threadOutput{Action: "subscribe", Error: err.Error()}, nil
				}
				return threadOutput{Success: true, Action: "subscribe", Result: res,
					Message: "已订阅。该线程之后的新事件会作为新的一轮送到本会话。"}, nil
			case "unsubscribe":
				if err := h.UnsubscribeThread(ctx, sid, in.Key); err != nil {
					return threadOutput{Action: "unsubscribe", Error: err.Error()}, nil
				}
				return threadOutput{Success: true, Action: "unsubscribe", Message: "已取消订阅"}, nil
			case "list":
				res, err := h.ListThreads(ctx, sid)
				if err != nil {
					return threadOutput{Action: "list", Error: err.Error()}, nil
				}
				return threadOutput{Success: true, Action: "list", Result: res}, nil
			default:
				return threadOutput{Action: in.Action, Error: fmt.Sprintf("unknown action %q, use subscribe/unsubscribe/list", in.Action)}, nil
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
