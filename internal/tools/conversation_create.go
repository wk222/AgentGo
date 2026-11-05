package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
)

// ConversationHandler is implemented by the host, which owns the conversations
// and the library of experts.
type ConversationHandler interface {
	// CreateConversation opens an empty conversation. expert is an id or name from
	// ListExperts, or "" for an ordinary conversation.
	CreateConversation(ctx context.Context, fromID, name, expert string) (any, error)
	ListExperts(ctx context.Context) (any, error)
}

var (
	conversationMu      sync.RWMutex
	conversationHandler ConversationHandler
)

// SetConversationHandler wires the host's conversations into the conversation_create and list_experts tools.
func SetConversationHandler(h ConversationHandler) {
	conversationMu.Lock()
	conversationHandler = h
	conversationMu.Unlock()
}

type conversationCreateInput struct {
	Name   string `json:"name" jsonschema:"description=Title of the new conversation\\, shown in the sidebar (at most 80 characters)"`
	Expert string `json:"expert,omitempty" jsonschema:"description=Run the new conversation as this expert: an id or a name from list_experts (for example financial-modeler). Leave empty for an ordinary conversation. An unknown expert returns the list of experts"`
}

type conversationCreateOutput struct {
	Success bool   `json:"success"`
	Result  any    `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type listExpertsInput struct{}

type listExpertsOutput struct {
	Success bool   `json:"success"`
	Experts any    `json:"experts,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterConversationTools registers `conversation_create` and `list_experts`.
// The first only creates: it sends no message and the user's screen stays where it is,
// so handing the new conversation its first task is a separate, visible step.
func RegisterConversationTools(r *Registry) error {
	create, err := utils.InferTool("conversation_create",
		"Open a NEW, empty conversation in the sidebar, optionally set up to work as one of the experts (list_experts shows them). "+
			"Use it only when the user asks you to open or start a conversation, or to put a piece of work in a conversation of its own. "+
			"It creates the conversation and nothing else: no message is sent and the user's screen does not switch to it. "+
			"To give it work, call session_message with action=send and to=<the returned id>. "+
			"It uses the same workspace as this conversation. Creating many conversations in a short time is refused.",
		func(ctx context.Context, in conversationCreateInput) (conversationCreateOutput, error) {
			conversationMu.RLock()
			h := conversationHandler
			conversationMu.RUnlock()
			if h == nil {
				return conversationCreateOutput{Error: "conversations cannot be created in this host"}, nil
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				return conversationCreateOutput{Error: "name is required"}, nil
			}
			res, err := h.CreateConversation(ctx, governance.SessionIDFromContext(ctx), name, strings.TrimSpace(in.Expert))
			if err != nil {
				return conversationCreateOutput{Error: err.Error()}, nil
			}
			return conversationCreateOutput{Success: true, Result: res,
				Message: "已创建。它还是空的：要交代工作，用 session_message（action=send, to=上面的 id）。"}, nil
		},
	)
	if err != nil {
		return err
	}
	list, err := utils.InferTool("list_experts",
		"List the experts a conversation can run as: id, name, what it is for, and the skills it relies on. "+
			"An expert is a role with standing rules; pass its id or name as `expert` to conversation_create. "+
			"Use it when the user asks which experts exist or which one fits a job.",
		func(ctx context.Context, _ listExpertsInput) (listExpertsOutput, error) {
			conversationMu.RLock()
			h := conversationHandler
			conversationMu.RUnlock()
			if h == nil {
				return listExpertsOutput{Error: "experts are not available in this host"}, nil
			}
			res, err := h.ListExperts(ctx)
			if err != nil {
				return listExpertsOutput{Error: fmt.Sprint(err)}, nil
			}
			return listExpertsOutput{Success: true, Experts: res}, nil
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(create)
	r.AddTool(list)
	return nil
}
