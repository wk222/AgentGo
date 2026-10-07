package bridge

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	agentdb "agentgo/internal/db"
	"agentgo/internal/engine"
	"agentgo/internal/sessions"

	_ "modernc.org/sqlite"
)

// runLogEngine plays a fixed run: stream, one tool call, a file change, an
// optional pause for approval.
type runLogEngine struct {
	pending bool
	failure string
	bigOut  int
}

func (runLogEngine) ID() string { return "runlog" }

func (e runLogEngine) Run(_ context.Context, _ engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	// The Router emits the "started" status itself; engines only report progress.
	em.Token("hel")
	em.Reasoning("thinking")
	em.ToolCall("call_1", "code_edit", `{"file_path":"a.go"}`)
	out := "ok"
	if e.bigOut > 0 {
		out = strings.Repeat("x", e.bigOut)
	}
	em.ToolResult("call_1", "code_edit", out, false)
	em.FileChange("a.go", "BEFORE-CONTENT", "AFTER-CONTENT", "@@ -1 +1 @@")
	em.Token("lo")
	res := engine.RunResult{Error: e.failure, Pending: e.pending}
	if e.failure == "" {
		res.Messages = []engine.Message{{Role: "assistant", Type: "text", Content: "hello"}}
	}
	return res, nil
}

func newLoggedService(t *testing.T, eng engine.AgentEngine) (*AppService, string) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	st, err := sessions.Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &AppService{rt: &Runtime{sessions: st}, engines: engine.NewRouter()}
	s.engines.Register(eng)
	sess, err := st.Create(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	return s, sess.ID
}

func kinds(evs []sessions.RunEvent) string {
	ks := make([]string, len(evs))
	for i, e := range evs {
		ks[i] = e.Kind
	}
	return strings.Join(ks, ",")
}

func TestRunLogKeepsTheDurableFactsAndCommitsLast(t *testing.T) {
	s, sid := newLoggedService(t, runLogEngine{})
	var live []string
	res := s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "runlog", SessionID: sid, Input: "go"},
		engine.SinkFunc(func(e engine.Event) { live = append(live, string(e.Type)) }))
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	if !strings.Contains(strings.Join(live, ","), "token") {
		t.Fatalf("live subscribers must still get every event, saw %v", live)
	}

	evs, err := s.rt.Sessions().ListRunEvents(context.Background(), sid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kinds(evs), "status,tool_call,tool_result,file_change,done"; got != want {
		t.Fatalf("log = %s, want %s (deltas must not be stored)", got, want)
	}
	if evs[1].CallID != "call_1" || evs[1].Name != "code_edit" || evs[2].CallID != "call_1" {
		t.Fatalf("tool events must carry the call id: %+v", evs)
	}
	if fc := evs[3].PayloadJSON; strings.Contains(fc, "BEFORE-CONTENT") || strings.Contains(fc, "AFTER-CONTENT") || !strings.Contains(fc, "@@ -1 +1 @@") {
		t.Fatalf("file change should keep the diff, not whole files: %s", fc)
	}

	// The commit point implies the final message is already stored.
	state, _ := s.rt.Sessions().LastRunState(context.Background(), sid)
	if state.Status != sessions.RunCompleted || state.RunID != res.RunID {
		t.Fatalf("state = %+v", state)
	}
	msgs, _ := s.rt.Sessions().GetMessages(context.Background(), sid, 10)
	if len(msgs) != 2 || msgs[1].Content != "hello" {
		t.Fatalf("transcript = %+v", msgs)
	}
}

func TestRunLogRecordsPauseAndFailureOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		eng  runLogEngine
		want sessions.RunStatus
	}{
		{"awaiting approval", runLogEngine{pending: true}, sessions.RunAwaitingApproval},
		{"failed", runLogEngine{failure: "model unreachable"}, sessions.RunFailed},
	} {
		s, sid := newLoggedService(t, tc.eng)
		s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "runlog", SessionID: sid, Input: "go"}, nil)
		state, err := s.rt.Sessions().LastRunState(context.Background(), sid)
		if err != nil || state.Status != tc.want {
			t.Fatalf("%s: state=%+v err=%v", tc.name, state, err)
		}
	}
}

func TestRunLogCutsHugeOutputButKeepsTheEvent(t *testing.T) {
	s, sid := newLoggedService(t, runLogEngine{bigOut: 1 << 20})
	s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "runlog", SessionID: sid, Input: "go"}, nil)
	evs, _ := s.rt.Sessions().ListRunEvents(context.Background(), sid, 0, 0)
	var result sessions.RunEvent
	for _, e := range evs {
		if e.Kind == sessions.KindToolResult {
			result = e
		}
	}
	if len(result.PayloadJSON) > runLogPayloadMax+256 || !strings.Contains(result.PayloadJSON, runLogCutMark) {
		t.Fatalf("payload not bounded: %d bytes", len(result.PayloadJSON))
	}
}

func TestRunLogWithoutASessionIsANoOp(t *testing.T) {
	s, _ := newLoggedService(t, runLogEngine{})
	res := s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "runlog", Input: "go"}, nil)
	if res.Error != "" {
		t.Fatal(res.Error)
	}
}

func TestCutStringNeverSplitsARune(t *testing.T) {
	s := strings.Repeat("中", 10) // 3 bytes each
	for max := 1; max < 30; max++ {
		got := strings.TrimSuffix(cutString(s, max), runLogCutMark)
		if got != "" && strings.ToValidUTF8(got, "\uFFFD") != got {
			t.Fatalf("max=%d produced invalid UTF-8: %q", max, got)
		}
	}
}

func TestGetRunEventsReportsEventsAndState(t *testing.T) {
	s, sid := newLoggedService(t, runLogEngine{})
	s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "runlog", SessionID: sid, Input: "go"}, nil)

	all := s.GetRunEvents(sid, 0, 0)
	evs := all["events"].([]sessions.RunEvent)
	if all["success"] != true || len(evs) != 5 {
		t.Fatalf("all = %+v", all)
	}
	// Reconnect after the tool result: only the rest comes back.
	rest := s.GetRunEvents(sid, evs[2].Seq, 0)["events"].([]sessions.RunEvent)
	if kinds(rest) != "file_change,done" {
		t.Fatalf("resume = %s", kinds(rest))
	}
	if r := s.GetRunEvents("  ", 0, 0); r["success"] != false {
		t.Fatalf("empty session id must be rejected: %v", r)
	}
}
