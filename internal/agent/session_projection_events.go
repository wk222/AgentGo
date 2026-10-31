package agent

import (
	"agentgo/internal/sessions"
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"reflect"
)

func durableProjectionMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		if m.Role == schema.System && m.Extra["_eino_adk_runtime_generated_system_message"] == true {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Persist the actual model-visible history rewrite. Without this event, Eino
// would replay the pre-compaction tool history on the next turn or after restart.
func recordProjectionRewrite(ctx context.Context, before, after []*schema.Message) error {
	if _, managed := adk.RunnerSessionID(ctx); !managed {
		return nil
	}
	previous := durableProjectionMessages(before)
	messages := durableProjectionMessages(after)
	// Preserve caller-supplied system messages folded into the generated prompt.
	var preservedSystems []*schema.Message
	for _, old := range previous {
		if old.Role != schema.System {
			continue
		}
		found := false
		for _, next := range messages {
			if next.Role == schema.System && next.Content == old.Content {
				found = true
				break
			}
		}
		if !found {
			preservedSystems = append(preservedSystems, old)
		}
	}
	messages = append(preservedSystems, messages...)
	// Reuse identity for unchanged host snapshots instead of inventing a new
	// logical message on every model call.
	for i, m := range messages {
		if sessions.ContextKind(m) != "runtime" {
			continue
		}
		for _, old := range previous {
			if sessions.ContextKind(old) == "runtime" && old.Content == m.Content {
				m.Extra = cloneMessageExtra(old.Extra, m.Extra)
				messages[i] = m
				break
			}
		}
	}
	for _, m := range messages {
		adk.EnsureMessageID(m)
	}
	if reflect.DeepEqual(previous, messages) {
		return nil
	}
	return adk.TypedSendEvent(ctx, &adk.AgentEvent{SessionEventVariant: &adk.SessionEventVariant[*schema.Message]{Event: &adk.SessionEvent[*schema.Message]{Kind: adk.SessionEventMessagesReplaced, Extra: map[string]any{"agentgo.reason": "runtime_projection"}, MessagesReplaced: &messages}}})
}

func cloneMessageExtra(previous, current map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range previous {
		out[k] = v
	}
	for k, v := range current {
		out[k] = v
	}
	return out
}
