package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"agentgo/internal/engine"
)

// fakeBackend scripts the brain: each Run pops the next script step.
type fakeBackend struct {
	mu       sync.Mutex
	runs     []func(sink engine.EventSink) engine.RunResult
	approves []Result
	answers  []Result

	inputs      []string
	approvedIDs map[string]bool
	answered    map[string]string
	cancelled   []string
	sessions    []SessionInfo
	pending     []Message
	created     int
	closed      bool
	block       chan struct{} // when set, Run waits for it (or Cancel)
}

func newFake() *fakeBackend {
	return &fakeBackend{approvedIDs: map[string]bool{}, answered: map[string]string{}}
}

func (f *fakeBackend) Run(sid, input string, sink engine.EventSink) engine.RunResult {
	f.mu.Lock()
	f.inputs = append(f.inputs, sid+"|"+input)
	var step func(engine.EventSink) engine.RunResult
	if len(f.runs) > 0 {
		step, f.runs = f.runs[0], f.runs[1:]
	}
	blk := f.block
	f.mu.Unlock()
	if blk != nil {
		<-blk
		return engine.RunResult{Reason: "interrupted"}
	}
	if step == nil {
		return engine.RunResult{Error: "no script"}
	}
	return step(sink)
}

func (f *fakeBackend) Approve(id string, ok bool) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvedIDs[id] = ok
	var r Result
	if len(f.approves) > 0 {
		r, f.approves = f.approves[0], f.approves[1:]
	}
	return r
}

func (f *fakeBackend) Answer(id, text string) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered[id] = text
	var r Result
	if len(f.answers) > 0 {
		r, f.answers = f.answers[0], f.answers[1:]
	}
	return r
}

func (f *fakeBackend) Cancel(sid string) {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, sid)
	blk := f.block
	f.block = nil
	f.mu.Unlock()
	if blk != nil {
		close(blk)
	}
}

func (f *fakeBackend) NewSession(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	return "s" + string(rune('0'+f.created)), nil
}
func (f *fakeBackend) Sessions() ([]SessionInfo, error) { return f.sessions, nil }
func (f *fakeBackend) Plugins() []PluginInfo {
	return []PluginInfo{{Name: "officedoc", State: "running"}, {Name: "bad", State: "failed", Error: "boom"}}
}
func (f *fakeBackend) Pending() []Message { return f.pending }
func (f *fakeBackend) Tools() []string    { return []string{"create_docx", "read_file"} }
func (f *fakeBackend) Close()             { f.closed = true }

func say(text string) func(engine.EventSink) engine.RunResult {
	return func(sink engine.EventSink) engine.RunResult {
		for _, w := range strings.SplitAfter(text, " ") {
			sink.Emit(engine.Event{Type: engine.EventToken, Payload: map[string]any{"delta": w}})
		}
		return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: text}}}
	}
}

func pauseFor(tool, args, id string) func(engine.EventSink) engine.RunResult {
	return func(sink engine.EventSink) engine.RunResult {
		sink.Emit(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"name": "read_file", "arguments": `{"p":1}`}})
		sink.Emit(engine.Event{Type: engine.EventToolResult, Payload: map[string]any{"name": "read_file", "output": "ok\nsecond line"}})
		return engine.RunResult{Pending: true, Messages: []engine.Message{
			{Role: "assistant", Type: "approval", ApprovalID: id, ToolName: tool, Arguments: args, Status: "pending"},
		}}
	}
}

type harness struct {
	be       *fakeBackend
	out, err bytes.Buffer
}

func (h *harness) run(t *testing.T, args []string, stdin string, tty bool) int {
	t.Helper()
	env := IO{In: strings.NewReader(stdin), Out: &h.out, Err: &h.err, InTTY: tty}
	return Main(args, env, func(Config) (Backend, error) { return h.be, nil })
}

func newHarness() *harness { return &harness{be: newFake()} }

func TestRunStreamsAnswerToStdoutAndCommentaryToStderr(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{func(sink engine.EventSink) engine.RunResult {
		sink.Emit(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"name": "read_file", "arguments": `{"path":"a.txt"}`}})
		sink.Emit(engine.Event{Type: engine.EventToolResult, Payload: map[string]any{"name": "read_file", "output": "hello\nworld"}})
		return say("The answer is 42.")(sink)
	}}
	if code := h.run(t, []string{"run", "what is it"}, "", true); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	if got := h.out.String(); got != "The answer is 42.\n" {
		t.Fatalf("stdout = %q", got)
	}
	if e := h.err.String(); !strings.Contains(e, "▸ read_file") || !strings.Contains(e, "✓ hello world") {
		t.Fatalf("commentary missing from stderr: %q", e)
	}
	if !h.be.closed {
		t.Fatal("backend not closed")
	}
	if !strings.HasSuffix(h.be.inputs[0], "|what is it") {
		t.Fatalf("input = %v", h.be.inputs)
	}
}

func TestRunWithoutStreamingPrintsFinalMessage(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{func(engine.EventSink) engine.RunResult {
		return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: "done"}}}
	}}
	h.run(t, []string{"do it"}, "", true) // bare words are a task
	if h.out.String() != "done\n" {
		t.Fatalf("stdout = %q", h.out.String())
	}
}

func TestRunTaskFromStdinAndArgs(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{say("ok")}
	h.run(t, []string{"run", "summarize"}, "line one\nline two\n", false)
	if got := h.be.inputs[0]; !strings.HasSuffix(got, "|summarize\n\nline one\nline two") {
		t.Fatalf("input = %q", got)
	}
}

func TestRunNoTaskIsUsageError(t *testing.T) {
	h := newHarness()
	if code := h.run(t, []string{"run"}, "", true); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestRunErrorExit(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{func(engine.EventSink) engine.RunResult {
		return engine.RunResult{Error: "model unreachable"}
	}}
	if code := h.run(t, []string{"run", "x"}, "", true); code != ExitError {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.err.String(), "model unreachable") {
		t.Fatalf("stderr = %q", h.err.String())
	}
}

func TestNonInteractiveApprovalIsNotGuessed(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{"relative_path":"a.docx"}`, "ap1")}
	code := h.run(t, []string{"run", "make a doc"}, "", false)
	if code != ExitNeedsAnswer {
		t.Fatalf("exit %d, want %d", code, ExitNeedsAnswer)
	}
	if len(h.be.approvedIDs) != 0 {
		t.Fatalf("decided without asking: %v", h.be.approvedIDs)
	}
	if !strings.Contains(h.err.String(), "create_docx") || !strings.Contains(h.err.String(), "-yolo") {
		t.Fatalf("stderr = %q", h.err.String())
	}
}

func TestYoloApprovesChainedPauses(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{}`, "ap1")}
	h.be.approves = []Result{
		{Messages: []Message{{Role: "assistant", Type: "approval", ApprovalID: "ap2", ToolName: "create_xlsx", Status: "pending"}}},
		{Messages: []Message{{Role: "assistant", Type: "text", Content: "both files written"}}},
	}
	if code := h.run(t, []string{"run", "-yolo", "docs"}, "", false); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	if !h.be.approvedIDs["ap1"] || !h.be.approvedIDs["ap2"] {
		t.Fatalf("approvals = %v", h.be.approvedIDs)
	}
	if h.out.String() != "both files written\n" {
		t.Fatalf("stdout = %q", h.out.String())
	}
}

func TestEndOfInputAtApprovalPromptDenies(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{"a":1}`, "ap1")}
	if code := h.run(t, []string{"run", "x"}, "", true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if ok, asked := h.be.approvedIDs["ap1"]; !asked || ok {
		t.Fatalf("EOF must record a denial, got asked=%v approved=%v", asked, ok)
	}
}

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func chatWith(be *fakeBackend, stdin string, yolo bool) (*Chat, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	c := &Chat{
		B: be, In: bufioReader(stdin), Prompt: &errb, Yolo: yolo, Ask: true,
		R: &Renderer{Out: &out, Err: &errb},
	}
	return c, &out, &errb
}

func TestApprovalAnswers(t *testing.T) {
	for _, tc := range []struct {
		in       string
		approved bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}, {"whatever\n", false}} {
		be := newFake()
		be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{}`, "ap1")}
		be.approves = []Result{{Messages: []Message{{Role: "assistant", Type: "text", Content: "r"}}}}
		c, _, errb := chatWith(be, tc.in, false)
		out := c.Turn("go")
		if got := be.approvedIDs["ap1"]; got != tc.approved {
			t.Errorf("input %q: approved=%v want %v", tc.in, got, tc.approved)
		}
		if !out.OK() {
			t.Errorf("input %q: outcome %+v", tc.in, out)
		}
		if !strings.Contains(errb.String(), "Allow create_docx") {
			t.Errorf("no prompt shown: %q", errb.String())
		}
	}
}

func TestAlwaysTurnsOnYoloForLaterPauses(t *testing.T) {
	be := newFake()
	be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{}`, "ap1")}
	be.approves = []Result{
		{Messages: []Message{{Type: "approval", ApprovalID: "ap2", ToolName: "create_xlsx", Status: "pending"}}},
		{},
	}
	c, _, _ := chatWith(be, "a\n", false) // one answer only: the second pause must not prompt
	if out := c.Turn("go"); !out.OK() {
		t.Fatalf("outcome %+v", out)
	}
	if !be.approvedIDs["ap1"] || !be.approvedIDs["ap2"] || !c.Yolo {
		t.Fatalf("approvals=%v yolo=%v", be.approvedIDs, c.Yolo)
	}
}

func TestQuestionIsAnswered(t *testing.T) {
	be := newFake()
	be.runs = []func(engine.EventSink) engine.RunResult{func(engine.EventSink) engine.RunResult {
		return engine.RunResult{Pending: true, Messages: []engine.Message{
			{Role: "assistant", Type: "question", ApprovalID: "q1", Content: "Which sheet?", Status: "pending"}}}
	}}
	be.answers = []Result{{Messages: []Message{{Role: "assistant", Type: "text", Content: "using Q3"}}}}
	c, out, _ := chatWith(be, "Q3\n", false)
	if o := c.Turn("go"); !o.OK() {
		t.Fatalf("outcome %+v", o)
	}
	if be.answered["q1"] != "Q3" || out.String() != "using Q3\n" {
		t.Fatalf("answered=%v out=%q", be.answered, out.String())
	}
}

func TestResolveErrorSurfaces(t *testing.T) {
	be := newFake()
	be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{}`, "ap1")}
	be.approves = []Result{{Error: "approval expired"}}
	c, _, _ := chatWith(be, "", true)
	if o := c.Turn("go"); o.Error != "approval expired" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestJSONModeEmitsOnlyEventLines(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{say("hi there")}
	if code := h.run(t, []string{"run", "-json", "x"}, "", true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		if v["type"] == nil {
			t.Fatalf("line without type: %q", l)
		}
	}
	if strings.Contains(h.out.String(), `"type":"message"`) {
		t.Fatalf("streamed answer was repeated as a message: %s", h.out.String())
	}
	if strings.Contains(h.err.String(), "session") {
		t.Fatalf("json mode printed a session banner: %q", h.err.String())
	}
}

func TestContinueAndSessionFlags(t *testing.T) {
	h := newHarness()
	h.be.sessions = []SessionInfo{{ID: "newest"}, {ID: "older"}}
	h.be.runs = []func(engine.EventSink) engine.RunResult{say("a"), say("b")}
	h.run(t, []string{"run", "-continue", "x"}, "", true)
	h.run(t, []string{"run", "-session", "older", "y"}, "", true)
	if !strings.HasPrefix(h.be.inputs[0], "newest|") || !strings.HasPrefix(h.be.inputs[1], "older|") {
		t.Fatalf("inputs = %v", h.be.inputs)
	}
	if h.be.created != 0 {
		t.Fatalf("created %d sessions", h.be.created)
	}
	if code := h.run(t, []string{"run", "-session", "a", "-continue", "z"}, "", true); code != ExitUsage {
		t.Fatalf("conflicting flags exit %d", code)
	}
}

func TestContinueWithNothingSaved(t *testing.T) {
	h := newHarness()
	if code := h.run(t, []string{"run", "-continue", "x"}, "", true); code != ExitError {
		t.Fatalf("exit %d", code)
	}
}

func TestOpenFailure(t *testing.T) {
	var out, errb bytes.Buffer
	code := Main([]string{"plugins"}, IO{In: strings.NewReader(""), Out: &out, Err: &errb},
		func(Config) (Backend, error) { return nil, errors.New("db locked") })
	if code != ExitError || !strings.Contains(errb.String(), "db locked") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}

func TestInspectionCommands(t *testing.T) {
	h := newHarness()
	h.be.sessions = []SessionInfo{{ID: "s1", Title: "Weekly report", Messages: 4, UpdatedAt: 1700000000}}
	h.run(t, []string{"sessions"}, "", true)
	h.run(t, []string{"plugins"}, "", true)
	h.run(t, []string{"tools"}, "", true)
	got := h.out.String()
	for _, want := range []string{"s1\t", "Weekly report", "officedoc", "bad", "boom", "create_docx"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestLeftoverApprovalsCanBeDecidedLater(t *testing.T) {
	left := []Message{
		{Type: "approval", ApprovalID: "ap1", ToolName: "create_docx", Arguments: `{"a":1}`, Status: "pending"},
		{Type: "approval", ApprovalID: "ap2", ToolName: "create_xlsx", Status: "pending"},
	}
	h := newHarness()
	h.be.pending = left
	h.run(t, []string{"approvals"}, "", false)
	if !strings.Contains(h.out.String(), "ap1\tcreate_docx") || !strings.Contains(h.out.String(), "ap2\tcreate_xlsx") {
		t.Fatalf("listing = %q", h.out.String())
	}

	h = newHarness()
	h.be.pending = left
	h.be.approves = []Result{{Messages: []Message{{Role: "assistant", Type: "text", Content: "resumed"}}}}
	if code := h.run(t, []string{"approve", "ap1"}, "", false); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	if !h.be.approvedIDs["ap1"] || len(h.be.approvedIDs) != 1 || h.out.String() != "resumed\n" {
		t.Fatalf("approved=%v out=%q", h.be.approvedIDs, h.out.String())
	}

	h = newHarness()
	h.be.pending = left
	h.run(t, []string{"deny", "all"}, "", false)
	if ok1, a1 := h.be.approvedIDs["ap1"]; !a1 || ok1 {
		t.Fatalf("ap1 = %v/%v", a1, ok1)
	}
	if ok2, a2 := h.be.approvedIDs["ap2"]; !a2 || ok2 {
		t.Fatalf("ap2 = %v/%v", a2, ok2)
	}

	h = newHarness()
	h.be.pending = left
	if code := h.run(t, []string{"approve", "nope"}, "", false); code != ExitError || len(h.be.approvedIDs) != 0 {
		t.Fatalf("exit %d approved=%v", code, h.be.approvedIDs)
	}
	if code := newHarness().run(t, []string{"approve"}, "", false); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestWorkspaceIsPinnedOnlyWhereItMatters(t *testing.T) {
	var got []Config
	open := func(c Config) (Backend, error) { got = append(got, c); return newFake(), nil }
	env := func() IO {
		return IO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, InTTY: true}
	}
	Main([]string{"sessions"}, env(), open)
	Main([]string{"run", "x"}, env(), open)
	Main([]string{"run", "-w", "D:/proj", "x"}, env(), open)
	cwd, _ := os.Getwd()
	if got[0].Workspace != "" || got[1].Workspace != cwd || got[2].Workspace != "D:/proj" {
		t.Fatalf("workspaces = %+v (cwd %s)", got, cwd)
	}
}

func TestHelpAndVersion(t *testing.T) {
	h := newHarness()
	if h.run(t, []string{"help"}, "", true) != ExitOK || !strings.Contains(h.out.String(), "Usage:") {
		t.Fatal("help")
	}
	h = newHarness()
	if h.run(t, []string{"version"}, "", true) != ExitOK || !strings.Contains(h.out.String(), "agentgo-cli") {
		t.Fatal("version")
	}
}

func TestReplSessionLifecycle(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{say("one"), say("two"), say("three")}
	h.be.sessions = []SessionInfo{{ID: "old", Title: "Old chat"}}
	in := strings.Join([]string{
		"first",
		"/sessions",
		"/resume 1",
		"second",
		"/new",
		"third",
		"multi \\",
		"line",
		"/exit",
	}, "\n") + "\n"
	h.be.runs = append(h.be.runs, say("four"))
	if code := h.run(t, nil, in, true); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	want := []string{"s1|first", "old|second", "s2|third", "s2|multi \nline"}
	if len(h.be.inputs) != len(want) {
		t.Fatalf("inputs = %q", h.be.inputs)
	}
	for i := range want {
		if h.be.inputs[i] != want[i] {
			t.Errorf("input[%d] = %q, want %q", i, h.be.inputs[i], want[i])
		}
	}
	if !strings.Contains(h.out.String(), "one") || !strings.Contains(h.out.String(), "two") {
		t.Fatalf("stdout = %q", h.out.String())
	}
}

func TestReplCommandsAndEOF(t *testing.T) {
	h := newHarness()
	in := "/yolo\n/yolo off\n/thinking\n/bogus\n/resume 9\n/plugins\n/tools\n/help\n"
	if code := h.run(t, nil, in, true); code != ExitOK { // EOF ends the loop
		t.Fatalf("exit %d", code)
	}
	e := h.err.String()
	for _, want := range []string{"yolo on", "yolo off", "thinking on", "unknown command /bogus", "no such entry", "officedoc", "create_docx", "Commands:"} {
		if !strings.Contains(e, want) {
			t.Errorf("stderr missing %q:\n%s", want, e)
		}
	}
	if len(h.be.inputs) != 0 {
		t.Fatalf("commands reached the model: %v", h.be.inputs)
	}
}

func TestCancelStopsTheRunInProgress(t *testing.T) {
	be := newFake()
	be.block = make(chan struct{})
	c, _, _ := chatWith(be, "", false)
	c.Session = "s9"

	done := make(chan Outcome, 1)
	go func() { done <- c.Turn("long task") }()
	for { // wait until the run is blocked inside the backend
		be.mu.Lock()
		n := len(be.inputs)
		be.mu.Unlock()
		if n > 0 {
			break
		}
		runtime.Gosched()
	}
	if !c.Cancel() {
		t.Fatal("Cancel found no run")
	}
	out := <-done
	if !out.Interrupted || out.OK() {
		t.Fatalf("outcome %+v", out)
	}
	if len(be.cancelled) == 0 || be.cancelled[0] != "s9" {
		t.Fatalf("cancelled = %v", be.cancelled)
	}
	if c.Cancel() {
		t.Fatal("Cancel reported a run after it finished")
	}
}

func TestInterruptedExitCode(t *testing.T) {
	h := newHarness()
	h.be.runs = []func(engine.EventSink) engine.RunResult{func(engine.EventSink) engine.RunResult {
		return engine.RunResult{Reason: "interrupted"}
	}}
	if code := h.run(t, []string{"run", "x"}, "", true); code != ExitInterrupted {
		t.Fatalf("exit %d", code)
	}
}

func TestReasoningOnlyWhenAsked(t *testing.T) {
	emit := func(r *Renderer) {
		r.Emit(engine.Event{Type: engine.EventReasoning, Payload: map[string]any{"delta": "pondering"}})
	}
	var errb bytes.Buffer
	r := &Renderer{Out: &bytes.Buffer{}, Err: &errb}
	emit(r)
	if errb.Len() != 0 {
		t.Fatalf("reasoning shown without -thinking: %q", errb.String())
	}
	r.Thinking = true
	emit(r)
	if !strings.Contains(errb.String(), "pondering") {
		t.Fatalf("reasoning hidden with -thinking: %q", errb.String())
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a\n  b\t c", 50); got != "a b c" {
		t.Fatal(got)
	}
	if got := oneLine("你好世界你好", 4); got != "你好世界…" {
		t.Fatal(got)
	}
}

func TestColorOnlyWhenEnabled(t *testing.T) {
	var errb bytes.Buffer
	r := &Renderer{Out: &bytes.Buffer{}, Err: &errb}
	r.Emit(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"name": "x"}})
	if strings.Contains(errb.String(), "\x1b") {
		t.Fatal("ANSI escape without color")
	}
	errb.Reset()
	r.Color = true
	r.Emit(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"name": "x"}})
	if !strings.Contains(errb.String(), "\x1b[2m") {
		t.Fatal("no styling with color")
	}
}

func TestNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if supportsColor(os.Stderr) {
		t.Fatal("NO_COLOR ignored")
	}
}
