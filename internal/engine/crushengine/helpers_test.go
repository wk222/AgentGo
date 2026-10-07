package crushengine

import (
	"testing"
	"time"

	"agentgo/internal/engine"
)

type resultT struct {
	text   string
	errStr string
	err    error
}

func runReq(input, session string) engine.RunRequest {
	return engine.RunRequest{RunID: "run-" + input + "-" + session, SessionID: session, Input: input}
}

func firstText(ms []engine.Message) string {
	for _, m := range ms {
		if m.Type == "text" {
			return m.Content
		}
	}
	return ""
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("condition not met in time")
}
