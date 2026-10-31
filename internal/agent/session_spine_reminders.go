package agent

import (
	"agentgo/internal/sessions"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Framework middlewares (tool search, ...) insert "system reminder" messages in the middle
// of the conversation and use a marker on Message.Extra to know they already did, so they
// insert once. The spine rebuilds the whole message list on every model call; if it
// folded those messages into its projection the marker would be lost, the framework would
// insert the reminder again on the next call, and the prompt would grow with every call
// while its prefix kept changing (observed: +883 chars per call, 0% prefix-cache hits).
// So reminders bypass the projection and are put back at the same place, untouched.
//
// Two things keep that placement valid. The projection owns a few messages of its own
// (runtime context, tool feedback) that it renders after the conversation, so they are
// not counted: the same count on both sides keeps the anchor on real conversation. And a
// reminder is never placed between an assistant tool call and its results; providers
// reject that prompt with HTTP 400 and the whole run fails.

type spineReminder struct {
	msg   *schema.Message
	after int // conversation messages (not system, reminder or projection context) that follow it
}

func isSystemReminder(m *schema.Message) bool {
	if m == nil || m.Extra == nil {
		return false
	}
	v, _ := m.Extra[adk.MessageExtraKeySystemReminder].(bool)
	return v
}

// countsAsConversation reports whether m takes part in the "messages after the reminder"
// count: not a system message, not a reminder, not a context message the projection
// re-renders on every call.
func countsAsConversation(m *schema.Message) bool {
	return m != nil && m.Role != schema.System && !isSystemReminder(m) && sessions.ContextKind(m) == ""
}

// splitSystemReminders removes reminders from msgs and remembers where each one sat,
// counted from the end of the conversation (older messages may get pruned, newer ones
// are only ever appended after the reminder's position is first fixed).
func splitSystemReminders(msgs []*schema.Message) (rest []*schema.Message, reminders []spineReminder) {
	for i, m := range msgs {
		if !isSystemReminder(m) {
			rest = append(rest, m)
			continue
		}
		after := 0
		for _, n := range msgs[i+1:] {
			if countsAsConversation(n) {
				after++
			}
		}
		reminders = append(reminders, spineReminder{msg: m, after: after})
	}
	return rest, reminders
}

// restoreSystemReminders re-inserts reminders into the rendered messages, each one
// before the same number of trailing conversation messages it had before.
func restoreSystemReminders(out []*schema.Message, reminders []spineReminder) []*schema.Message {
	// Leading system messages (the projection) stay in front of everything.
	first := 0
	for first < len(out) && out[first] != nil && out[first].Role == schema.System {
		first++
	}
	// Last reminder (smallest "after") first, so earlier ones are located by counting
	// only conversation messages.
	for i := len(reminders) - 1; i >= 0; i-- {
		r := reminders[i]
		pos, cnt := len(out), 0
		for pos > first && cnt < r.after {
			pos--
			if countsAsConversation(out[pos]) {
				cnt++
			}
		}
		// Never split an assistant tool call from its results: step back to the
		// assistant message that issued them.
		for pos > first && pos < len(out) && out[pos] != nil && out[pos].Role == schema.Tool {
			pos--
		}
		out = append(out, nil)
		copy(out[pos+1:], out[pos:])
		out[pos] = r.msg
	}
	return out
}
