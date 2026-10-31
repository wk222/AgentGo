package bridge

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"agentgo/internal/userhooks"
)

// The two events that belong to a chat turn rather than to a tool call: the
// user sent a message (UserPromptSubmit), and the turn ended (Stop). Tool calls
// are covered by userToolHook in userhooks_plugin.go.

// hookContextTitle heads what a UserPromptSubmit hook adds to the model's input,
// in the same style as the blocks an @mention adds, so the model can tell it
// from what the user typed.
const hookContextTitle = "#### Context: user hook"

// fireUserHook runs the hooks of one event and logs the scripts that failed.
func (rt *Runtime) fireUserHook(ctx context.Context, ev userhooks.Event) userhooks.Outcome {
	out := rt.hooksEngine().Fire(ctx, ev) // a nil engine answers "nothing to do"
	logHookWarnings(out.Warnings)
	return out
}

// preparePrompt turns what the user typed into what the model receives. Hooks
// for UserPromptSubmit see the text as typed. One that refuses ends the turn
// here: the model is never called, and the reply says why. What a hook adds as
// context goes after the slash command and the @mentions have been expanded.
func (s *AppService) preparePrompt(ctx context.Context, sessionID, userText string) (modelInput string, refused *SendMessageResult) {
	return s.rt.preparePrompt(ctx, sessionID, userText)
}

func (rt *Runtime) preparePrompt(ctx context.Context, sessionID, userText string) (modelInput string, refused *SendMessageResult) {
	out := rt.fireUserHook(ctx, userhooks.Event{
		Name: userhooks.UserPromptSubmit, SessionID: sessionID, Cwd: rt.WorkspaceRoot(), Prompt: userText,
	})
	if out.Blocked {
		reply := promptRefusedReply(out)
		return "", &reply
	}
	return withHookContext(rt.expandUserInput(ctx, userText), out.Context), nil
}

func withHookContext(modelInput, hookContext string) string {
	hookContext = strings.TrimSpace(hookContext)
	if hookContext == "" {
		return modelInput
	}
	return modelInput + "\n\n" + hookContextTitle + "\n" + hookContext
}

// promptRefusedReply is shown in place of an answer. It reads as a notice, not
// as something the model said, and the message that was refused never reaches
// the model, so later turns do not see it either.
func promptRefusedReply(out userhooks.Outcome) SendMessageResult {
	text := "[钩子拦截] 这条消息没有发给模型。原因：" + out.Reason
	if out.By != "" {
		text += "\n（钩子：" + out.By + "）"
	}
	return SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: text}}}
}

// turnEnd reports whether a result is the end of a turn, and the last thing the
// assistant said. A turn that failed, or stopped to ask for an approval or an
// answer, has not ended: the Stop hook runs when it does.
func turnEnd(res SendMessageResult) (lastReply string, ended bool) {
	if res.Error != "" {
		return "", false
	}
	for _, m := range res.Messages {
		if m.Status == "pending" {
			return "", false
		}
		if m.Role == "assistant" && (m.Type == "" || m.Type == "text") {
			lastReply = m.Content
		}
	}
	return lastReply, true
}

// maxStopHooksInFlight bounds how many turns can have their Stop hooks still
// running. A hook that is slower than the chat piles up behind its own lock;
// beyond this the extra runs are skipped and logged instead of queueing forever.
const maxStopHooksInFlight = 8

var (
	stopHooksInFlight int32
	stopHooksWG       sync.WaitGroup // lets a test wait for the hooks it started
)

// fireStopHook runs the Stop hooks after a turn. It does not wait for them: the
// answer is already final, and a slow script must not make the chat feel slow.
func (rt *Runtime) fireStopHook(sessionID, lastReply string) {
	if !rt.hooksEngine().Has(userhooks.Stop) {
		return
	}
	if atomic.AddInt32(&stopHooksInFlight, 1) > maxStopHooksInFlight {
		atomic.AddInt32(&stopHooksInFlight, -1)
		log.Printf("[userhooks] %d Stop hooks are still running; skipped the one for session %s", maxStopHooksInFlight, sessionID)
		return
	}
	cwd := rt.WorkspaceRoot()
	stopHooksWG.Add(1)
	go func() {
		defer stopHooksWG.Done()
		defer atomic.AddInt32(&stopHooksInFlight, -1)
		rt.fireUserHook(context.Background(), userhooks.Event{
			Name: userhooks.Stop, SessionID: sessionID, Cwd: cwd, LastReply: lastReply,
		})
	}()
}
