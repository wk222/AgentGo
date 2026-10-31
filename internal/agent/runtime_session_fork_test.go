package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/adk"
)

// A fork is only worth having if the model, not just the transcript, carries on
// from the copy. This runs real Eino turns against the stored event log: the
// forked session has to be sent the original's history, and from then on the two
// conversations must not see each other.
func TestForkedSessionContinuesFromTheOriginalsHistory(t *testing.T) {
	ctx := context.Background()
	store, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "fork.db"))
	m := &runtimeCaptureModel{}
	spineRoot := t.TempDir()
	turn := func(sid, text string) map[string]int {
		t.Helper()
		ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name: "test", Description: "test", Model: m, Instruction: "base",
			Handlers: []adk.ChatModelAgentMiddleware{NewSessionSpineMiddleware(SessionSpineConfig{WorkspaceRoot: spineRoot, MaxActiveTurns: 30})},
		})
		if err != nil {
			t.Fatal(err)
		}
		it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag, SessionID: sid, SessionStore: store}).Query(ctx, text)
		for {
			e, ok := it.Next()
			if !ok {
				break
			}
			if e.Err != nil {
				t.Fatal(e.Err)
			}
		}
		seen := map[string]int{}
		for _, msg := range m.inputs[len(m.inputs)-1] {
			seen[msg.Content]++
		}
		return seen
	}
	expect := func(what string, seen map[string]int, want map[string]int) {
		t.Helper()
		for text, n := range want {
			if seen[text] != n {
				t.Fatalf("%s: %q appears %d times in the model input, want %d (%+v)", what, text, seen[text], n, seen)
			}
		}
	}

	if err := store.store.EnsureSession(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	turn("orig", "代号是 ORCHID-7，记住")
	res, err := store.store.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	fork := res.Session.ID
	if res.Events == 0 {
		t.Fatalf("a conversation with a finished turn has context events: %+v", res)
	}

	// The fork starts from everything the original had said, once each.
	expect("first turn in the fork", turn(fork, "我的代号是什么"), map[string]int{
		"代号是 ORCHID-7，记住": 1, "answer": 1, "我的代号是什么": 1,
	})
	// Neither side sees what the other adds afterwards.
	expect("original after the fork", turn("orig", "只发给原对话"), map[string]int{
		"代号是 ORCHID-7，记住": 1, "只发给原对话": 1, "我的代号是什么": 0,
	})
	expect("fork after the original moved on", turn(fork, "只发给分叉"), map[string]int{
		"代号是 ORCHID-7，记住": 1, "我的代号是什么": 1, "只发给分叉": 1, "只发给原对话": 0,
	})

	// The log the fork wrote is its own: the original keeps exactly its two turns.
	orig, err := store.LoadEvents(ctx, "orig", nil)
	if err != nil {
		t.Fatal(err)
	}
	forked, err := store.LoadEvents(ctx, fork, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(forked.Events) <= res.Events || len(orig.Events) <= res.Events {
		t.Fatalf("both logs should have grown past the %d copied events: orig=%d fork=%d", res.Events, len(orig.Events), len(forked.Events))
	}
	// Events carried over keep their identity, so a log can be told from a rewrite.
	for i := 0; i < res.Events; i++ {
		if orig.Events[i].EventID != forked.Events[i].EventID {
			t.Fatalf("event %d: %q vs %q", i, orig.Events[i].EventID, forked.Events[i].EventID)
		}
	}
}
