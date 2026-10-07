package crushproto

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// ScriptRunner plays one fixed turn without calling any model: reasoning, a
// `write` tool call that needs approval, its result, then streamed text. It
// exists to prove the protocol layer against the stock Crush client/TUI.
type ScriptRunner struct {
	// Pace is the delay between steps (default 150ms; tests use 1ms).
	Pace time.Duration
}

func (p ScriptRunner) pause(ctx context.Context) bool {
	d := p.Pace
	if d == 0 {
		d = 150 * time.Millisecond
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func (p ScriptRunner) Run(ctx context.Context, req RunRequest, em *Emitter) error {
	em.UserMessage(req.Prompt)

	// Turn 1: think, then ask to write a file.
	a1 := em.Assistant()
	a1.ReasoningStart()
	a1.AppendReasoning("Analyzing user request and formulating execution plan...")
	if !p.pause(ctx) {
		return ctx.Err()
	}
	a1.ReasoningDone()

	callID := "call_probe_" + newID()[:8]
	target := filepath.ToSlash(filepath.Join(em.ws.path, "hello.txt"))
	a1.ToolCallStart(callID, "write")
	if !p.pause(ctx) {
		return ctx.Err()
	}
	input, _ := json.Marshal(map[string]string{"file_path": target, "content": "hi"})
	a1.ToolCallInput(callID, string(input))

	granted := em.RequestPermission(PermissionRequest{
		ToolCallID:  callID,
		ToolName:    "write",
		Description: "Create file " + target,
		Action:      "write",
		Params:      map[string]string{"file_path": target, "new_content": "hi"},
		Path:        em.ws.path,
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var answer string
	if granted {
		em.ToolResult(callID, "write", "<result>\nFile successfully written (simulated): "+target+"\n</result>", "", false)
		answer = "This is the AgentGo protocol probe. No model was called: I pretended to create hello.txt and you approved it."
	} else {
		em.ToolResult(callID, "write", "User denied permission", "", true)
		answer = "This is the AgentGo protocol probe. The write was denied, so nothing was created."
	}
	a1.Finish("tool_use")

	// Turn 2: stream the final text.
	a2 := em.Assistant()
	for _, w := range strings.SplitAfter(answer, " ") {
		if !p.pause(ctx) {
			return ctx.Err()
		}
		a2.AppendText(w)
	}
	a2.Finish("end_turn")

	title := req.Prompt
	if r := []rune(title); len(r) > 40 {
		title = string(r[:40])
	}
	em.Complete(req, a2.ID(), a2.Text(), title, 10, len(answer)/4)
	return nil
}
