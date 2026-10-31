package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTurnMetaMergesAndLaterWins(t *testing.T) {
	ctx := WithTurnMeta(context.Background(), map[string]any{"checkpoint": "cp_1", "x": 1})
	ctx = WithTurnMeta(ctx, map[string]any{"checkpoint": "cp_2"})
	got := TurnMetaFrom(ctx)
	if got["checkpoint"] != "cp_2" || got["x"] != 1 {
		t.Fatalf("meta = %v", got)
	}
	if TurnMetaFrom(context.Background()) != nil {
		t.Fatal("no meta expected on a plain context")
	}
	if WithTurnMeta(ctx, nil) != ctx {
		t.Fatal("adding nothing must return the same context")
	}
}

// The checkpoint id has to survive a reload of the session, so it is stored in
// the user message's meta next to the images.
func TestTranscriptBeginStoresTurnMetaWithTheUserMessage(t *testing.T) {
	st := openTestStore(t)
	tr := NewTranscript(st)
	ctx := WithTurnMeta(context.Background(), map[string]any{"checkpoint": "cp_9"})
	tr.Begin(ctx, "s", "with meta", []string{"a.png"})
	tr.Begin(context.Background(), "s", "plain", nil)

	rows, err := st.GetMessages(context.Background(), "s", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(rows[0].MetaJSON), &meta); err != nil {
		t.Fatalf("meta %q: %v", rows[0].MetaJSON, err)
	}
	if meta["checkpoint"] != "cp_9" || meta["images"] == nil {
		t.Fatalf("meta = %v", meta)
	}
	if strings.Contains(rows[1].MetaJSON, "checkpoint") {
		t.Fatalf("a turn without meta must stay without: %q", rows[1].MetaJSON)
	}
}

// A rollback note is part of the history the model reads on the next turn.
func TestTranscriptNoteIsReplayedToTheModel(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	tr := NewTranscript(st)
	tr.Begin(ctx, "s", "fix it", nil)
	tr.Finish("s", []Message{{Role: "assistant", Content: "done"}})
	tr.Note("s", "【系统记录】文件已回退", map[string]any{"kind": "workspace_rollback"})
	tr.Note("s", "   ", nil) // blank notes are ignored
	tr.Note("", "no session", nil)

	h := tr.History(ctx, "s")
	if len(h) != 3 || h[2].Role != schema.Assistant || h[2].Content != "【系统记录】文件已回退" {
		t.Fatalf("history = %+v", h)
	}
	rows, err := st.GetMessages(ctx, "s", 10)
	if err != nil || len(rows) != 3 || !strings.Contains(rows[2].MetaJSON, "workspace_rollback") {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}
