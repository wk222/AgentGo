package sessions

import "github.com/cloudwego/eino/schema"

// ExtraContextKind identifies host-created context. User text is never a type tag.
const ExtraContextKind = "agentgo.context_kind.v1"

func ContextMessage(kind, content string) *schema.Message {
	m := schema.UserMessage(content)
	m.Extra = map[string]any{ExtraContextKind: kind}
	return m
}

func ContextKind(m *schema.Message) string {
	if m == nil || m.Extra == nil {
		return ""
	}
	kind, _ := m.Extra[ExtraContextKind].(string)
	return kind
}

func IsContextMessage(m *schema.Message) bool { return ContextKind(m) != "" }
