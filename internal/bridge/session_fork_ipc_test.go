package bridge

import (
	"context"
	"testing"
	"time"

	"agentgo/internal/core"
	"agentgo/internal/engine"
	"agentgo/internal/sessions"
)

func seedForkSource(t *testing.T, store *sessions.Store, title string) string {
	t.Helper()
	ctx := context.Background()
	sess, err := store.Create(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range [][2]string{{"user", "把配置读取拆出去"}, {"assistant", "先看 config.go"}, {"user", "好，开始"}} {
		if err := store.AppendMessage(ctx, sess.ID, m[0], m[1], "text", nil); err != nil {
			t.Fatal(err)
		}
	}
	events := []sessions.ContextEventRecord{
		{ID: "e1", Kind: "message", Payload: []byte("one")},
		{ID: "e2", Kind: "message", Payload: []byte("two")},
	}
	if err := store.AppendContextEvents(ctx, sess.ID, "scope", events, false); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func TestForkSessionCopiesTheConversationAndListsItAsAFork(t *testing.T) {
	svc, store, _ := sessionService(t)
	orig := seedForkSource(t, store, "重构方案")

	res := svc.ForkSession(" "+orig+" ", "")
	if res["success"] != true {
		t.Fatalf("answer: %v", res)
	}
	fork, _ := res["id"].(string)
	if fork == "" || fork == orig {
		t.Fatalf("id: %v", res["id"])
	}
	if res["messages"] != 3 || res["events"] != 2 || res["tasks"] != 0 || res["parent_id"] != orig || res["parent_title"] != "重构方案" {
		t.Fatalf("answer: %v", res)
	}
	if w, ok := res["warnings"].([]string); !ok || len(w) != 0 {
		t.Fatalf("warnings must be an empty list: %#v", res["warnings"])
	}
	if sess, ok := res["session"].(sessions.Session); !ok || sess.ID != fork || sess.Title != "重构方案（分叉）" || sess.MessageCount != 3 {
		t.Fatalf("session: %#v", res["session"])
	}

	// The sidebar list says where the fork came from, and only for forks.
	byID := map[string]map[string]any{}
	for _, row := range svc.ListSessions() {
		byID[row["id"].(string)] = row
	}
	if len(byID) != 2 {
		t.Fatalf("listed: %v", byID)
	}
	if row := byID[fork]; row["parent_id"] != orig || row["parent_title"] != "重构方案" || row["parent_gone"] != false || row["message_count"] != 3 {
		t.Fatalf("fork row: %v", row)
	}
	if _, has := byID[orig]["parent_id"]; has {
		t.Fatalf("the original claims a parent: %v", byID[orig])
	}

	// The page reads the fork like any other conversation.
	msgs := svc.GetSessionMessages(fork)
	if msgs["session_id"] != fork || msgs["count"] != 3 {
		t.Fatalf("messages: %v", msgs)
	}

	// Deleting the original leaves the fork and says so.
	if got := svc.DeleteSession(orig); got["success"] != true {
		t.Fatal(got)
	}
	found := false
	for _, row := range svc.ListSessions() {
		if row["id"] == fork {
			found = true
			if row["parent_gone"] != true || row["parent_title"] != "重构方案" {
				t.Fatalf("fork row after the original was deleted: %v", row)
			}
		}
	}
	if !found {
		t.Fatal("the fork went with the original")
	}
}

func TestForkSessionAnswersPlainlyWhenItCannot(t *testing.T) {
	svc, store, _ := sessionService(t)
	seedForkSource(t, store, "还在")
	for _, c := range []struct{ id, wantErr string }{
		{"sess_gone", "这条对话已经不存在"},
		{"   ", "empty session_id"},
	} {
		if got := svc.ForkSession(c.id, ""); got["success"] != false || got["error"] != c.wantErr {
			t.Errorf("ForkSession(%q) = %v, want error %q", c.id, got, c.wantErr)
		}
	}
	if got := (&AppService{rt: &Runtime{}}).ForkSession("sess_a", ""); got["success"] != false {
		t.Errorf("without a store: %v", got)
	}
	if n := len(svc.ListSessions()); n != 1 {
		t.Fatalf("a refused fork left %d sessions", n)
	}
}

// blockingEngine holds a run open until released, like a model that is still answering.
type blockingEngine struct {
	started chan struct{}
	release chan struct{}
}

func (blockingEngine) ID() string { return "block" }

func (e blockingEngine) Run(ctx context.Context, _ engine.RunRequest, _ engine.Emitter) (engine.RunResult, error) {
	close(e.started)
	select {
	case <-e.release:
	case <-ctx.Done():
	}
	return engine.RunResult{}, nil
}

func TestForkSessionRefusesAConversationThatIsBusy(t *testing.T) {
	svc, store, _ := sessionService(t)
	ctx := context.Background()
	orig := seedForkSource(t, store, "忙着")
	other := seedForkSource(t, store, "闲着")
	want := 2 // sessions that should exist
	refuse := func(why string) {
		t.Helper()
		got := svc.ForkSession(orig, "")
		if got["success"] != false || got["error"] != why || got["busy"] != true {
			t.Fatalf("want a refusal %q, got %v", why, got)
		}
		if n := len(svc.ListSessions()); n != want {
			t.Fatalf("a refused fork left %d sessions, want %d", n, want)
		}
	}

	// Paused in this process, waiting for an approval.
	svc.rt.pending = core.NewPendingStore()
	svc.rt.pending.Set(core.PendingRun{SessionID: orig, ApprovalID: "ap-1"})
	refuse(forkBusyApproval)
	// Another conversation's pause is none of this one's business.
	if got := svc.ForkSession(other, ""); got["success"] != true {
		t.Fatalf("an idle conversation was refused: %v", got)
	}
	want++
	svc.rt.pending.Delete("ap-1")

	// Paused in another process: only the database row knows.
	if err := store.SavePendingRun(ctx, sessions.PendingRun{ApprovalID: "ap-2", SessionID: orig, OwnerPID: 1, PayloadJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	svc.rt.pending = nil
	refuse(forkBusyApproval)
	if err := store.DeletePendingRun(ctx, "ap-2"); err != nil {
		t.Fatal(err)
	}

	// Running right now.
	eng := blockingEngine{started: make(chan struct{}), release: make(chan struct{})}
	svc.rt.engineRouter = engine.NewRouter()
	svc.rt.engineRouter.Register(eng)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.rt.engineRouter.Run(ctx, engine.RunRequest{Engine: "block", SessionID: orig, Input: "go"}, nil)
	}()
	select {
	case <-eng.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never started")
	}
	refuse(forkBusyRunning)
	close(eng.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never finished")
	}

	// Idle again: it forks.
	if got := svc.ForkSession(orig, ""); got["success"] != true {
		t.Fatalf("after the run: %v", got)
	}
}
