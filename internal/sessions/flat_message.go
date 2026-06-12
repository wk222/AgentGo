package sessions

import "github.com/cloudwego/eino/schema"

// FlatMessage is an active-turn message with optional native tool calls (for Render).
type FlatMessage struct {
	Role                  string
	Content               string
	ToolCallID            string
	ToolCalls             []schema.ToolCall
	UserInputMultiContent []schema.MessageInputPart
}
