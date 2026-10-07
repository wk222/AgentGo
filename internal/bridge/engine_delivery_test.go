package bridge

import (
	"context"
	"testing"

	"agentgo/internal/engine"
	"agentgo/internal/sessions"
)

func TestDesktopCompletionFollowsTranscriptCommit(t *testing.T) {
	s, sid := newLoggedService(t, runLogEngine{})
	done := 0
	s.runEngineTurn(context.Background(), engine.RunRequest{SessionID: sid, Input: "go"}, engine.SinkFunc(func(ev engine.Event) {
		if ev.Type != engine.EventDone {
			return
		}
		done++
		rows, err := s.rt.Sessions().GetMessages(context.Background(), sid, 100)
		if err != nil || len(rows) != 2 || rows[1].Content != "hello" {
			t.Errorf("completion preceded transcript: rows=%v err=%v", rows, err)
		}
		facts, err := s.rt.Sessions().ListRunEvents(context.Background(), sid, 0, 0)
		if err != nil || len(facts) == 0 || facts[len(facts)-1].Kind != sessions.KindDone {
			t.Errorf("completion preceded durable commit: facts=%v err=%v", facts, err)
		}
	}))
	if done != 1 {
		t.Fatalf("done count = %d", done)
	}
}

func TestDesktopResumeSequencesDoNotRestart(t *testing.T) {
	s := &AppService{}
	sink := s.wailsSink()
	first, finish := engine.ResumeEmitter(sink, "same-run", "session", "eino")
	first.Token("first")
	finish(engine.RunResult{Pending: true})
	second, finish := engine.ResumeEmitter(sink, "same-run", "session", "eino")
	second.Token("second")
	finish(engine.RunResult{})
	if got := s.desktopEventSeq.Load(); got != 4 {
		t.Fatalf("sequence = %d", got)
	}
}

func TestReloadedApprovalUsesRecordedDecision(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_reload")
	err := g.s.rt.Sessions().AppendMessage(context.Background(), g.sid, "assistant", "approve?", "approval",
		map[string]any{"approval_id": id, "status": "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if out := g.s.resolveApproval(context.Background(), id, false, "", "test", ""); out["success"] != true {
		t.Fatal(out)
	}
	rows := g.s.GetSessionMessages(g.sid)["messages"].([]map[string]any)
	if len(rows) != 1 || rows[0]["resolved"] != true || rows[0]["status"] != "rejected" {
		t.Fatalf("stale approval: %v", rows)
	}
}
