package core

import (
	"context"
	"database/sql"
	"testing"

	"github.com/cloudwego/eino/schema"
	_ "modernc.org/sqlite"

	agentdb "agentgo/internal/db"
	"agentgo/internal/sessions"
)

func openTestStore(t *testing.T) *sessions.Store {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	st, err := sessions.Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A session id that arrives from outside (HTTP gateway) has no row yet: the
// transcript must create it, and the next turn must see the earlier ones.
func TestTranscriptSecondTurnSeesFirstForUnknownSession(t *testing.T) {
	ctx := context.Background()
	tr := NewTranscript(openTestStore(t))

	if h := tr.History(ctx, "gw-1"); len(h) != 0 {
		t.Fatalf("fresh session has history: %v", h)
	}

	tr.Begin(ctx, "gw-1", "记住代号 蓝鲸-7319", nil)
	tr.Finish("gw-1", []Message{{Role: "assistant", Content: "好的"}})

	h := tr.History(ctx, "gw-1")
	if len(h) != 2 || h[0].Role != schema.User || h[0].Content != "记住代号 蓝鲸-7319" ||
		h[1].Role != schema.Assistant || h[1].Content != "好的" {
		t.Fatalf("second turn would not see the first: %+v", h)
	}
}

// Begin must hand the run the history WITHOUT the message of the turn it starts.
func TestTranscriptBeginExcludesCurrentUserMessage(t *testing.T) {
	ctx := context.Background()
	tr := NewTranscript(openTestStore(t))
	tr.Begin(ctx, "s", "first", nil)
	tr.Finish("s", []Message{{Role: "assistant", Content: "a1"}})

	before := tr.History(ctx, "s")
	tr.Begin(ctx, "s", "second", nil)
	if len(before) != 2 {
		t.Fatalf("history before second turn = %d messages, want 2", len(before))
	}
	after := tr.History(ctx, "s")
	if len(after) != 3 || after[2].Content != "second" {
		t.Fatalf("user message not stored: %+v", after)
	}
}

func TestTranscriptApprovalRowsAreStoredButNotReplayed(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	tr := NewTranscript(st)
	tr.Begin(ctx, "s", "run it", nil)
	tr.Finish("s", []Message{{Role: "assistant", Type: "approval", Content: "need ok",
		ApprovalID: "ap1", ToolName: "execute_bash", Arguments: "{}", Status: "pending"}})

	rows, err := st.GetMessages(ctx, "s", 10)
	if err != nil || len(rows) != 2 || rows[1].Type != "approval" || rows[1].MetaJSON == "" {
		t.Fatalf("approval row not stored with meta: %+v err=%v", rows, err)
	}
	if h := tr.History(ctx, "s"); len(h) != 1 {
		t.Fatalf("approval rows must not be replayed to the model: %+v", h)
	}
}

func TestTranscriptNilLogIsInert(t *testing.T) {
	tr := NewTranscript(nil)
	ctx := tr.Begin(context.Background(), "s", "x", nil)
	tr.Finish("s", []Message{{Role: "assistant", Content: "y"}})
	if ctx == nil || tr.History(ctx, "s") != nil {
		t.Fatal("nil log must disable persistence without panicking")
	}
}
