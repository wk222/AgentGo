package cli

import (
	"strings"
	"testing"

	"agentgo/internal/engine"
)

// streamingFake can also stream the run a decision continues (cli.Streamer).
type streamingFake struct{ *fakeBackend }

func (s streamingFake) ApproveStream(id string, ok bool, sink engine.EventSink) Result {
	s.Approve(id, ok) // record the decision
	sink.Emit(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"id": "c9", "name": "create_docx", "arguments": `{"title":"t"}`}})
	sink.Emit(engine.Event{Type: engine.EventToolResult, Payload: map[string]any{"id": "c9", "name": "create_docx", "output": "written"}})
	sink.Emit(engine.Event{Type: engine.EventToken, Payload: map[string]any{"delta": "file "}})
	sink.Emit(engine.Event{Type: engine.EventToken, Payload: map[string]any{"delta": "written"}})
	return Result{Messages: []Message{{Role: "assistant", Type: "text", Content: "file written"}}}
}

func (s streamingFake) AnswerStream(id, text string, sink engine.EventSink) Result {
	return s.Answer(id, text)
}

func runStreaming(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	be := streamingFake{newFake()}
	be.runs = []func(engine.EventSink) engine.RunResult{pauseFor("create_docx", `{}`, "ap1")}
	var h harness
	h.be = be.fakeBackend
	env := IO{In: strings.NewReader(""), Out: &h.out, Err: &h.err}
	if code := Main(args, env, func(Config) (Backend, error) { return be, nil }); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	if !be.approvedIDs["ap1"] {
		t.Fatalf("approval not recorded: %v", be.approvedIDs)
	}
	return h.out.String(), h.err.String()
}

// The continued run's tool call and reply reach the terminal as events, and the
// final message does not print the reply a second time.
func TestResumedRunIsStreamedToTheTerminal(t *testing.T) {
	out, errOut := runStreaming(t, "run", "-yolo", "docs")
	if out != "file written\n" {
		t.Fatalf("stdout = %q, want the reply once", out)
	}
	if !strings.Contains(errOut, "▸ create_docx") || !strings.Contains(errOut, "written") {
		t.Fatalf("the unblocked tool call is not shown:\n%s", errOut)
	}
}

func TestResumedRunEventsAppearInJSONMode(t *testing.T) {
	out, _ := runStreaming(t, "run", "-yolo", "-json", "docs")
	if !strings.Contains(out, `"name":"create_docx"`) {
		t.Fatalf("tool_call of the resumed run missing from the JSON stream:\n%s", out)
	}
	if strings.Contains(out, `"type":"message"`) {
		t.Fatalf("the reply was streamed as tokens; the final message must not repeat it:\n%s", out)
	}
}

func TestApprovalsListQuestionsAndApproveAllSkipsThem(t *testing.T) {
	h := newHarness()
	h.be.pending = []Message{
		{Role: "assistant", Type: "question", ApprovalID: "q1", ToolName: "ask_user", Arguments: "session s1", Content: "red or blue?", Status: "pending"},
		{Role: "assistant", Type: "approval", ApprovalID: "ap1", ToolName: "create_docx", Arguments: `{}`, Status: "pending"},
	}
	if code := h.run(t, []string{"approvals"}, "", false); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	out := h.out.String()
	if !strings.Contains(out, "q1\tquestion\tred or blue?") || !strings.Contains(out, "ap1\tcreate_docx") {
		t.Fatalf("approvals output:\n%s", out)
	}

	h = newHarness()
	h.be.pending = []Message{
		{Role: "assistant", Type: "question", ApprovalID: "q1", ToolName: "ask_user", Content: "red or blue?", Status: "pending"},
		{Role: "assistant", Type: "approval", ApprovalID: "ap1", ToolName: "create_docx", Status: "pending"},
	}
	h.be.approves = []Result{{Messages: []Message{{Role: "assistant", Type: "text", Content: "done"}}}}
	if code := h.run(t, []string{"approve", "all"}, "", false); code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, h.err.String())
	}
	if _, asked := h.be.approvedIDs["q1"]; asked || !h.be.approvedIDs["ap1"] {
		t.Fatalf("approve all must settle the approval and leave the question: %v", h.be.approvedIDs)
	}
	if !strings.Contains(h.err.String(), "skipping question q1") {
		t.Fatalf("no note about the skipped question:\n%s", h.err.String())
	}
}
