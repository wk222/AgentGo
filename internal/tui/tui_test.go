package tui

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"agentgo/internal/cli"
	"agentgo/internal/engine"
)

// fakeBE is a scripted cli.Backend (and Historian).
type fakeBE struct {
	mu      sync.Mutex
	run     func(session, input string, sink engine.EventSink) engine.RunResult
	approve func(id string, ok bool) cli.Result
	stream  func(sink engine.EventSink) cli.Result // ApproveStream

	streamAnswer func(sink engine.EventSink) cli.Result // AnswerStream
	answer       func(id, text string) cli.Result
	decisions    []string
	answers      []string
	cancelled    []string
	sessions     []cli.SessionInfo
	history      map[string][]cli.Message
}

func (f *fakeBE) Run(s, in string, sink engine.EventSink) engine.RunResult { return f.run(s, in, sink) }
func (f *fakeBE) Approve(id string, ok bool) cli.Result {
	f.mu.Lock()
	f.decisions = append(f.decisions, id+":"+map[bool]string{true: "yes", false: "no"}[ok])
	f.mu.Unlock()
	if f.approve != nil {
		return f.approve(id, ok)
	}
	return cli.Result{}
}

// ApproveStream makes the fake a Streamer; without f.stream it behaves as Approve.
func (f *fakeBE) ApproveStream(id string, ok bool, sink engine.EventSink) cli.Result {
	if f.stream == nil {
		return f.Approve(id, ok)
	}
	f.mu.Lock()
	f.decisions = append(f.decisions, id+":"+map[bool]string{true: "yes", false: "no"}[ok])
	f.mu.Unlock()
	return f.stream(sink)
}

// AnswerStream: same idea for questions, through f.streamAnswer.
func (f *fakeBE) AnswerStream(id, text string, sink engine.EventSink) cli.Result {
	if f.streamAnswer == nil {
		return f.Answer(id, text)
	}
	f.mu.Lock()
	f.answers = append(f.answers, id+":"+text)
	f.mu.Unlock()
	return f.streamAnswer(sink)
}
func (f *fakeBE) Answer(id, text string) cli.Result {
	f.mu.Lock()
	f.answers = append(f.answers, id+":"+text)
	f.mu.Unlock()
	if f.answer != nil {
		return f.answer(id, text)
	}
	return cli.Result{}
}
func (f *fakeBE) Cancel(s string)                      { f.mu.Lock(); f.cancelled = append(f.cancelled, s); f.mu.Unlock() }
func (f *fakeBE) NewSession(string) (string, error)    { return "sess-1", nil }
func (f *fakeBE) Sessions() ([]cli.SessionInfo, error) { return f.sessions, nil }
func (f *fakeBE) Plugins() []cli.PluginInfo {
	return []cli.PluginInfo{{Name: "codetools", State: "running"}}
}
func (f *fakeBE) Pending() []cli.Message                   { return nil }
func (f *fakeBE) Tools() []string                          { return []string{"view", "edit"} }
func (f *fakeBE) Close()                                   {}
func (f *fakeBE) History(id string) ([]cli.Message, error) { return f.history[id], nil }

func ev(t engine.EventType, p map[string]any) engine.Event { return engine.Event{Type: t, Payload: p} }

func answerRun(text string) func(string, string, engine.EventSink) engine.RunResult {
	return func(_, _ string, sink engine.EventSink) engine.RunResult {
		sink.Emit(ev(engine.EventToken, map[string]any{"delta": text}))
		return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: text}}}
	}
}

func pendingApproval(id, tool, args string) engine.Message {
	return engine.Message{Role: "assistant", Type: "approval", Status: "pending", ApprovalID: id, ToolName: tool, Arguments: args}
}

// harness drives a Model synchronously: it runs each returned command and
// feeds the message back, skipping the refresh ticks.
type harness struct {
	t    *testing.T
	m    *Model
	quit bool
}

func newHarness(t *testing.T, be *fakeBE, o Options) *harness {
	m := New(be, o)
	h := &harness{t: t, m: m}
	h.pump(m.Init()) // focuses the input, as the program would
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.m.Update(msg)
	h.pump(cmd)
}

func (h *harness) pump(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch x := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range x {
			h.pump(c)
		}
	case tickMsg:
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(x)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}
func (h *harness) enter()       { h.send(tea.KeyPressMsg{Code: tea.KeyEnter}) }
func (h *harness) press(r rune) { h.send(tea.KeyPressMsg{Code: r, Text: string(r)}) }
func (h *harness) say(s string) { h.typeText(s); h.enter() }
func (h *harness) view() string { return h.m.View().Content }
func (h *harness) kinds() []kind {
	var out []kind
	for _, e := range h.m.tl.entries {
		out = append(out, e.kind)
	}
	return out
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func (h *harness) mustView(sub string) {
	h.t.Helper()
	if v := ansi.ReplaceAllString(h.view(), ""); !strings.Contains(v, sub) {
		h.t.Fatalf("view lacks %q:\n%s", sub, v)
	}
}

func TestStreamingTurnBuildsTimeline(t *testing.T) {
	be := &fakeBE{run: func(_, _ string, sink engine.EventSink) engine.RunResult {
		sink.Emit(ev(engine.EventToken, map[string]any{"delta": "Hel"}))
		sink.Emit(ev(engine.EventToken, map[string]any{"delta": "lo"}))
		sink.Emit(ev(engine.EventToolCall, map[string]any{"id": "c1", "name": "view", "arguments": `{"file_path":"a.go"}`}))
		sink.Emit(ev(engine.EventToolResult, map[string]any{"id": "c1", "name": "view", "output": "package m", "is_error": false}))
		sink.Emit(ev(engine.EventFileChange, map[string]any{"path": "a.go", "unified_diff": "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"}))
		return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: "Hello"}}}
	}}
	h := newHarness(t, be, Options{Workspace: "/w/proj"})
	h.say("hi")

	if h.m.mode != modeIdle {
		t.Fatalf("mode = %v, want idle", h.m.mode)
	}
	if h.m.session != "sess-1" {
		t.Errorf("session = %q", h.m.session)
	}
	want := []kind{kUser, kAssistant, kTool, kDiff}
	got := h.kinds()
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	// The streamed text must not be repeated from the final message.
	if n := strings.Count(h.view(), "Hello"); n != 1 {
		t.Errorf("assistant text shown %d times", n)
	}
	h.mustView("▸ view")
	h.mustView("± a.go")
	h.mustView("+new")
	h.mustView("proj")
}

func TestApprovalShowsDiffPreviewAndResumes(t *testing.T) {
	args := `{"file_path":"a.go","old_string":"x := 1","new_string":"x := 2"}`
	be := &fakeBE{
		run: func(string, string, engine.EventSink) engine.RunResult {
			return engine.RunResult{Pending: true, Messages: []engine.Message{pendingApproval("ap1", "code_edit", args)}}
		},
		approve: func(string, bool) cli.Result {
			return cli.Result{Messages: []cli.Message{{Role: "assistant", Type: "text", Content: "edited"}}}
		},
	}
	h := newHarness(t, be, Options{})
	h.say("change it")

	if h.m.mode != modeApprove {
		t.Fatalf("mode = %v, want approve", h.m.mode)
	}
	h.mustView("Allow code_edit?")
	h.mustView("- x := 1")
	h.mustView("+ x := 2")

	h.press('y')
	if len(be.decisions) != 1 || be.decisions[0] != "ap1:yes" {
		t.Fatalf("decisions = %v", be.decisions)
	}
	if h.m.mode != modeIdle {
		t.Fatalf("mode after approval = %v", h.m.mode)
	}
	h.mustView("edited")
}

func TestDenyAndAlways(t *testing.T) {
	n := 0
	be := &fakeBE{
		run: func(string, string, engine.EventSink) engine.RunResult {
			return engine.RunResult{Pending: true, Messages: []engine.Message{pendingApproval("p1", "bash", `{"command":"rm -rf x"}`)}}
		},
		approve: func(id string, ok bool) cli.Result {
			n++
			if n == 1 { // the resumed run pauses again
				return cli.Result{Messages: []cli.Message{{Type: "approval", Status: "pending", ApprovalID: "p2", ToolName: "bash", Arguments: `{"command":"ls"}`}}}
			}
			return cli.Result{}
		},
	}
	h := newHarness(t, be, Options{})
	h.say("clean")
	h.mustView("$ rm -rf x")
	h.press('n')
	if be.decisions[0] != "p1:no" {
		t.Fatalf("decisions = %v", be.decisions)
	}
	h.mustView("denied bash")

	// The second pause: 'a' approves it and everything after.
	if h.m.mode != modeApprove {
		t.Fatalf("mode = %v, want approve for the follow-up pause", h.m.mode)
	}
	h.press('a')
	if !h.m.yolo || be.decisions[1] != "p2:yes" {
		t.Fatalf("yolo=%v decisions=%v", h.m.yolo, be.decisions)
	}
	h.mustView("YOLO")
}

func TestYoloOptionAutoApproves(t *testing.T) {
	be := &fakeBE{run: func(string, string, engine.EventSink) engine.RunResult {
		return engine.RunResult{Pending: true, Messages: []engine.Message{pendingApproval("p1", "write", `{"file_path":"n.txt","content":"hi"}`)}}
	}}
	h := newHarness(t, be, Options{Yolo: true})
	h.say("go")
	if len(be.decisions) != 1 || be.decisions[0] != "p1:yes" || h.m.mode != modeIdle {
		t.Fatalf("decisions=%v mode=%v", be.decisions, h.m.mode)
	}
	h.mustView("auto-approved write")
}

func TestQuestionIsAnsweredFromTheInputBox(t *testing.T) {
	be := &fakeBE{
		run: func(string, string, engine.EventSink) engine.RunResult {
			return engine.RunResult{Pending: true, Messages: []engine.Message{{Role: "assistant", Type: "question", Status: "pending", ApprovalID: "q1", Content: "Which file?"}}}
		},
		answer: func(string, string) cli.Result {
			return cli.Result{Messages: []cli.Message{{Role: "assistant", Type: "text", Content: "thanks"}}}
		},
	}
	h := newHarness(t, be, Options{})
	h.say("help")
	if h.m.mode != modeAsk {
		t.Fatalf("mode = %v, want ask", h.m.mode)
	}
	h.mustView("Which file?")
	h.say("b.go")
	if len(be.answers) != 1 || be.answers[0] != "q1:b.go" {
		t.Fatalf("answers = %v", be.answers)
	}
	h.mustView("thanks")
}

func TestEscCancelsRunningTurn(t *testing.T) {
	release := make(chan struct{})
	be := &fakeBE{run: func(string, string, engine.EventSink) engine.RunResult {
		<-release
		return engine.RunResult{Reason: "interrupted"}
	}}
	h := newHarness(t, be, Options{})
	h.typeText("long task")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if h.m.mode != modeBusy {
		t.Fatalf("mode = %v, want busy", h.m.mode)
	}
	done := make(chan tea.Msg, 4)
	for _, c := range cmd().(tea.BatchMsg) {
		go func() { done <- c() }()
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(be.cancelled) != 1 || be.cancelled[0] != "sess-1" {
		// the run goroutine creates the session first; wait for it
		close(release)
		for range 2 {
			if m, ok := (<-done).(runDoneMsg); ok {
				h.send(m)
			}
		}
		t.Fatalf("cancelled = %v", be.cancelled)
	}
	close(release)
	for range 2 {
		if m, ok := (<-done).(runDoneMsg); ok {
			h.send(m)
		}
	}
	if h.m.mode != modeIdle {
		t.Fatalf("mode = %v, want idle after cancel", h.m.mode)
	}
	h.mustView("cancelled")
}

func TestSlashCommands(t *testing.T) {
	h := newHarness(t, &fakeBE{run: answerRun("x")}, Options{})
	h.say("/yolo on")
	if !h.m.yolo {
		t.Error("/yolo on did not enable yolo")
	}
	h.say("/thinking")
	if !h.m.tl.thinking {
		t.Error("/thinking did not toggle")
	}
	h.say("/nope")
	h.mustView("unknown command /nope")
	h.say("/plugins")
	h.mustView("codetools")
	h.say("/help")
	h.mustView("Commands")
	h.press('x') // any key closes help
	if h.m.help {
		t.Error("help still open")
	}
	h.say("/new")
	h.mustView("new conversation")
	h.say("/exit")
	if !h.quit {
		t.Error("/exit did not quit")
	}
}

func TestSessionPickerResumesWithHistory(t *testing.T) {
	be := &fakeBE{
		sessions: []cli.SessionInfo{{ID: "s-old", Title: "older", Messages: 4}, {ID: "s-new", Title: "newer", Messages: 2}},
		history: map[string][]cli.Message{"s-new": {
			{Role: "user", Type: "text", Content: "earlier question"},
			{Role: "assistant", Type: "text", Content: "earlier answer"},
		}},
	}
	h := newHarness(t, be, Options{})
	h.say("/sessions")
	if h.m.mode != modePick {
		t.Fatalf("mode = %v, want pick", h.m.mode)
	}
	h.mustView("Resume a conversation")
	h.send(tea.KeyPressMsg{Code: tea.KeyDown})
	h.enter()
	if h.m.session != "s-new" || h.m.mode != modeIdle {
		t.Fatalf("session=%q mode=%v", h.m.session, h.m.mode)
	}
	h.mustView("earlier question")
	h.mustView("earlier answer")
}

func TestCtrlCTwiceExitsAndCtrlDExitsOnEmptyInput(t *testing.T) {
	h := newHarness(t, &fakeBE{run: answerRun("x")}, Options{})
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	h.send(ctrlC)
	if h.quit {
		t.Fatal("first Ctrl+C must only warn")
	}
	h.send(ctrlC)
	if !h.quit {
		t.Fatal("second Ctrl+C must exit")
	}

	h = newHarness(t, &fakeBE{run: answerRun("x")}, Options{})
	h.send(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if !h.quit {
		t.Fatal("Ctrl+D on an empty input must exit")
	}
}

func TestDiagnosticsAreHighlightedAndCounted(t *testing.T) {
	out := "<file_diagnostics>\nError: b.go:7:25 [gopls] cannot use \"x\" as int\nWarn: b.go:9:1 [gopls] unused\n</file_diagnostics>"
	be := &fakeBE{run: func(_, _ string, sink engine.EventSink) engine.RunResult {
		sink.Emit(ev(engine.EventToolCall, map[string]any{"id": "d1", "name": "lsp_diagnostics", "arguments": `{}`}))
		sink.Emit(ev(engine.EventToolResult, map[string]any{"id": "d1", "name": "lsp_diagnostics", "output": out}))
		return engine.RunResult{}
	}}
	h := newHarness(t, be, Options{})
	h.say("check")
	if h.m.diagErrs != 1 || h.m.diagWarns != 1 {
		t.Fatalf("diag counts = %d/%d", h.m.diagErrs, h.m.diagWarns)
	}
	h.mustView("cannot use")
	h.mustView("diagnostics 1E 1W")
}

func TestTinyWindowSaysSo(t *testing.T) {
	h := newHarness(t, &fakeBE{run: answerRun("x")}, Options{})
	h.send(tea.WindowSizeMsg{Width: 20, Height: 5})
	h.mustView("Window too small")
}

func TestPreviewShapes(t *testing.T) {
	cases := []struct{ tool, args, want string }{
		{"code_write", `{"file_path":"n.go","content":"a\nb"}`, "+ a\n+ b"},
		{"multiedit", `{"file_path":"m.go","edits":[{"old_string":"1","new_string":"2"},{"old_string":"3","new_string":"4"}]}`, "(2 edits)"},
		{"bash", `{"command":"go test ./..."}`, "$ go test ./..."},
		{"fetch", `{"url":"http://x"}`, `"url": "http://x"`},
	}
	for _, c := range cases {
		if got := preview(c.tool, c.args); !strings.Contains(got, c.want) {
			t.Errorf("preview(%s) = %q, want it to contain %q", c.tool, got, c.want)
		}
	}
	long := `{"file_path":"n.go","content":"` + strings.Repeat("x\\n", 100) + `"}`
	if got := preview("write", long); !strings.Contains(got, "more lines") {
		t.Errorf("long preview not capped: %d lines", strings.Count(got, "\n"))
	}
}

func TestMessageTypedWhileBusyIsQueuedAndSentAfterwards(t *testing.T) {
	var inputs []string
	be := &fakeBE{run: func(_, in string, sink engine.EventSink) engine.RunResult {
		inputs = append(inputs, in)
		return engine.RunResult{}
	}}
	h := newHarness(t, be, Options{})
	h.m.session = "s1"
	h.m.mode = modeBusy // a turn is running
	h.say("second")
	if len(h.m.queued) != 1 || len(inputs) != 0 {
		t.Fatalf("queued=%v inputs=%v", h.m.queued, inputs)
	}
	h.mustView("queued")
	h.say("/yolo") // commands must not slip in mid-turn
	if h.m.yolo {
		t.Fatal("a slash command ran while busy")
	}

	h.send(runDoneMsg{session: "s1", res: engine.RunResult{}}) // the turn ends
	if len(inputs) != 1 || inputs[0] != "second" {
		t.Fatalf("queued message not sent: %v", inputs)
	}

	h.m.mode = modeBusy
	h.say("third")
	h.send(runDoneMsg{session: "s1", res: engine.RunResult{Reason: "interrupted"}})
	if len(h.m.queued) != 0 || len(inputs) != 1 {
		t.Fatalf("a cancelled turn must drop the queue: queued=%v inputs=%v", h.m.queued, inputs)
	}
}

func TestApprovedRunStreamsItsToolAndReplyWithoutDuplicating(t *testing.T) {
	be := &fakeBE{
		run: func(_, _ string, sink engine.EventSink) engine.RunResult {
			sink.Emit(ev(engine.EventToken, map[string]any{"delta": "let me edit"}))
			return engine.RunResult{Pending: true, Messages: []engine.Message{pendingApproval("ap1", "code_edit", `{"file_path":"a.go"}`)}}
		},
		stream: func(sink engine.EventSink) cli.Result {
			sink.Emit(ev(engine.EventToolCall, map[string]any{"id": "c1", "name": "code_edit", "arguments": `{"file_path":"a.go"}`}))
			sink.Emit(ev(engine.EventToolResult, map[string]any{"id": "c1", "name": "code_edit", "output": "ok"}))
			sink.Emit(ev(engine.EventFileChange, map[string]any{"path": "a.go", "unified_diff": "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"}))
			sink.Emit(ev(engine.EventToken, map[string]any{"delta": "all done"}))
			return cli.Result{Messages: []cli.Message{{Role: "assistant", Type: "text", Content: "all done"}}}
		},
	}
	h := newHarness(t, be, Options{})
	h.say("edit it")
	h.press('y')

	v := ansi.ReplaceAllString(h.view(), "")
	for _, want := range []string{"code_edit", "+new", "all done"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q after the approved run:\n%s", want, v)
		}
	}
	if n := strings.Count(v, "all done"); n != 1 {
		t.Fatalf("reply shown %d times, want once:\n%s", n, v)
	}
}

func TestClosingAStreamedReplyRendersMarkdown(t *testing.T) {
	be := &fakeBE{run: answerRun("use **bold** here")}
	h := newHarness(t, be, Options{})
	h.say("hi")
	if v := ansi.ReplaceAllString(h.view(), ""); strings.Contains(v, "**bold**") {
		t.Fatalf("markdown markers still visible after the turn ended:\n%s", v)
	}
}

func TestAnsweredQuestionStreamsTheContinuedRun(t *testing.T) {
	be := &fakeBE{
		run: func(string, string, engine.EventSink) engine.RunResult {
			return engine.RunResult{Pending: true, Messages: []engine.Message{{Role: "assistant", Type: "question", Status: "pending", ApprovalID: "q1", Content: "Which file?"}}}
		},
		streamAnswer: func(sink engine.EventSink) cli.Result {
			sink.Emit(ev(engine.EventToolCall, map[string]any{"id": "c1", "name": "code_view", "arguments": `{"file_path":"b.go"}`}))
			sink.Emit(ev(engine.EventToolResult, map[string]any{"id": "c1", "name": "code_view", "output": "ok"}))
			sink.Emit(ev(engine.EventToken, map[string]any{"delta": "read b.go"}))
			return cli.Result{Messages: []cli.Message{{Role: "assistant", Type: "text", Content: "read b.go"}}}
		},
	}
	h := newHarness(t, be, Options{})
	h.say("help")
	h.say("b.go")
	v := ansi.ReplaceAllString(h.view(), "")
	if !strings.Contains(v, "code_view") {
		t.Fatalf("the tool the answer unblocked is not shown:\n%s", v)
	}
	if n := strings.Count(v, "read b.go"); n != 1 {
		t.Fatalf("reply shown %d times, want once:\n%s", n, v)
	}
}
