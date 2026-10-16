package agent

import (
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

type spineReminder struct {
	msg   *schema.Message
	after int // non-reminder, non-system messages that follow it
}

func isSystemReminder(m *schema.Message) bool {
	if m == nil || m.Extra == nil {
		return false
	}
	v, _ := m.Extra[adk.MessageExtraKeySystemReminder].(bool)
	return v
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
			if n != nil && !isSystemReminder(n) && n.Role != schema.System {
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
			if !isSystemReminder(out[pos]) {
				cnt++
			}
		}
		out = append(out, nil)
		copy(out[pos+1:], out[pos:])
		out[pos] = r.msg
	}
	return out
}
