package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentgo/internal/artifact"
	"agentgo/internal/sessions"
)

type fakeLog struct{ msgs []sessions.Message }

func (f *fakeLog) EnsureSession(context.Context, string) error { return nil }
func (f *fakeLog) GetMessages(context.Context, string, int) ([]sessions.Message, error) {
	return f.msgs, nil
}
func (f *fakeLog) AppendMessage(_ context.Context, sid, role, content, typ string, _ map[string]any) error {
	f.msgs = append(f.msgs, sessions.Message{SessionID: sid, Role: role, Content: content, Type: typ})
	return nil
}
func (f *fakeLog) AutoTitleFromUserMessage(context.Context, string, string) error { return nil }

func TestDeliveriesLiveInTheTranscriptAndStayOutOfModelHistory(t *testing.T) {
	log := &fakeLog{}
	dl := filepath.Join(t.TempDir(), "attachments")
	tr := NewTranscript(log).WithArtifactDir(dl)
	ctx := tr.RecordEvidence(context.Background(), "s1")

	src := filepath.Join(t.TempDir(), "r.txt")
	_ = os.WriteFile(src, []byte("hello"), 0o644)
	rec, err := artifact.Deliver(ctx, src, "t")
	if err != nil || rec.Version != 1 {
		t.Fatalf("deliver: %+v %v", rec, err)
	}
	if got := BuildHistory(log.msgs, 50, 100000); len(got) != 0 {
		t.Fatalf("delivery rows must not reach the model history: %d", len(got))
	}

	// 一个“重启后”的新 Transcript 从同一份记录里找回，同内容不重复。
	tr2 := NewTranscript(log).WithArtifactDir(dl)
	again, _ := artifact.Deliver(tr2.RecordEvidence(context.Background(), "s1"), src, "")
	if again.Status != artifact.StatusUnchanged || len(log.msgs) != 1 {
		t.Fatalf("after restart: %+v rows=%d", again, len(log.msgs))
	}
	list, _ := tr2.ListArtifacts(context.Background(), "s1")
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("list = %+v", list)
	}

	removed, found, err := tr2.RemoveArtifact(context.Background(), "s1", rec.ID)
	if err != nil || !found || removed.Status != artifact.StatusRemoved {
		t.Fatalf("remove: %+v %v %v", removed, found, err)
	}
	list, _ = tr2.ListArtifacts(context.Background(), "s1")
	if len(list) != 1 || list[0].Status != artifact.StatusRemoved {
		t.Fatalf("list after remove = %+v", list)
	}
	if _, found, _ := tr2.RemoveArtifact(context.Background(), "s1", "ar_nope"); found {
		t.Fatal("unknown id")
	}
}
