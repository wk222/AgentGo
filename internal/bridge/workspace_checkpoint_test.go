package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/engine"
	"agentgo/internal/workspace"
)

// editEngine plays an agent turn that rewrites one file of the workspace.
type editEngine struct{ root, rel, content string }

func (editEngine) ID() string { return "edit" }

func (e editEngine) Run(_ context.Context, _ engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	if err := os.WriteFile(filepath.Join(e.root, e.rel), []byte(e.content), 0o644); err != nil {
		return engine.RunResult{Error: err.Error()}, nil
	}
	em.Token("ok")
	return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: "edited"}}}, nil
}

// blockEngine holds a run open until released, like a long turn.
type blockEngine struct{ entered, release chan struct{} }

func (blockEngine) ID() string { return "block" }

func (e blockEngine) Run(ctx context.Context, _ engine.RunRequest, _ engine.Emitter) (engine.RunResult, error) {
	close(e.entered)
	select {
	case <-e.release:
	case <-ctx.Done():
	}
	return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: "slow"}}}, nil
}

func newCheckpointRig(t *testing.T) (s *AppService, sid, root string) {
	t.Helper()
	root = t.TempDir()
	writeRigFile(t, root, "a.txt", "before")
	s, sid = newLoggedService(t, editEngine{root: root, rel: "a.txt", content: "after"})
	s.rt.workspace = root
	s.rt.turnCheckpoints = newTurnCheckpointStore()
	return s, sid, root
}

func writeRigFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRigFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// runRigTurn runs one turn through the shared turn boundary and returns the
// payload of its "started" event.
func runRigTurn(t *testing.T, s *AppService, sid, engineID, input string) map[string]any {
	t.Helper()
	var started map[string]any
	res := s.runEngineTurn(context.Background(), engine.RunRequest{Engine: engineID, SessionID: sid, Input: input},
		engine.SinkFunc(func(ev engine.Event) {
			if ev.Type == engine.EventStatus && ev.Payload["state"] == "started" {
				started = ev.Payload
			}
		}))
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	return started
}

func rigMessages(s *AppService, sid string) []map[string]any {
	return s.GetSessionMessages(sid)["messages"].([]map[string]any)
}

func TestTurnCheckpointReachesTheClientAndTheTranscript(t *testing.T) {
	s, sid, _ := newCheckpointRig(t)
	started := runRigTurn(t, s, sid, "edit", "edit a.txt")
	id, _ := started["checkpoint_id"].(string)
	if id == "" || started["checkpoint_error"] != nil {
		t.Fatalf("started payload: %v", started)
	}

	rows := rigMessages(s, sid)
	if len(rows) < 1 || rows[0]["role"] != "user" || rows[0]["checkpoint"] != id {
		t.Fatalf("the user message must carry the checkpoint id after a reload: %v", rows)
	}
	list := s.ListTurnCheckpoints(sid)
	cps, _ := list["checkpoints"].([]workspace.CheckpointInfo)
	if list["success"] != true || list["enabled"] != true || len(cps) != 1 || cps[0].ID != id || cps[0].Prompt != "edit a.txt" {
		t.Fatalf("list: %v", list)
	}
}

func TestRollbackToTurnRestoresFilesTellsTheModelAndCanBeUndone(t *testing.T) {
	s, sid, root := newCheckpointRig(t)
	id := runRigTurn(t, s, sid, "edit", "edit a.txt")["checkpoint_id"].(string)
	if readRigFile(t, root, "a.txt") != "after" {
		t.Fatal("the turn should have edited the file")
	}

	prev := s.PreviewTurnRollback(sid, id)
	p, _ := prev["preview"].(*workspace.RollbackPreview)
	if prev["success"] != true || p == nil || p.Restore != 1 || prev["busy"] != "" {
		t.Fatalf("preview: %v", prev)
	}
	if readRigFile(t, root, "a.txt") != "after" {
		t.Fatal("a preview must not change files")
	}

	out := s.RollbackToTurn(sid, id)
	if out["success"] != true || readRigFile(t, root, "a.txt") != "before" {
		t.Fatalf("rollback: %v", out)
	}
	rows := rigMessages(s, sid)
	last := rows[len(rows)-1]
	if last["kind"] != noteKindRollback || !strings.Contains(last["content"].(string), "edit a.txt") {
		t.Fatalf("a note must follow the rollback in the conversation: %v", last)
	}
	history := transcriptOf(s.rt).History(context.Background(), sid)
	if n := history[len(history)-1].Content; !strings.Contains(n, "重新读取") {
		t.Fatalf("the model must be told to look at the files again: %q", n)
	}

	undo := s.RollbackToTurn(sid, out["undo_checkpoint_id"].(string))
	if undo["success"] != true || readRigFile(t, root, "a.txt") != "after" {
		t.Fatalf("undo: %v", undo)
	}
	rows = rigMessages(s, sid)
	if note := rows[len(rows)-1]["content"].(string); !strings.Contains(note, "撤销") {
		t.Fatalf("undo note: %q", note)
	}
	// Only real turns are offered in the list; the safety checkpoints are not.
	if cps, _ := s.ListTurnCheckpoints(sid)["checkpoints"].([]workspace.CheckpointInfo); len(cps) != 1 {
		t.Fatalf("list after undo: %v", cps)
	}
}

func TestRollbackIsRefusedWhileARunIsInProgress(t *testing.T) {
	s, sid, root := newCheckpointRig(t)
	id := runRigTurn(t, s, sid, "edit", "edit a.txt")["checkpoint_id"].(string)

	blocker := blockEngine{entered: make(chan struct{}), release: make(chan struct{})}
	s.rt.engineRouter.Register(blocker)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runEngineTurn(context.Background(), engine.RunRequest{Engine: "block", SessionID: sid, Input: "slow"}, nil)
	}()
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the second run never started")
	}

	out := s.RollbackToTurn(sid, id)
	if out["success"] != false || out["busy"] != true {
		t.Fatalf("rollback during a run: %v", out)
	}
	if prev := s.PreviewTurnRollback(sid, id); prev["busy"] == "" {
		t.Fatalf("the preview must say the rollback is blocked: %v", prev)
	}
	if readRigFile(t, root, "a.txt") != "after" {
		t.Fatal("a refused rollback must not touch files")
	}

	close(blocker.release)
	<-done
	if out := s.RollbackToTurn(sid, id); out["success"] != true {
		t.Fatalf("rollback once idle: %v", out)
	}
}

func TestTurnCheckpointNeverBlocksATurn(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		t.Setenv("AGENTGO_CHECKPOINTS", "0")
		s, sid, _ := newCheckpointRig(t)
		started := runRigTurn(t, s, sid, "edit", "go")
		if started["checkpoint_id"] != nil || started["checkpoint_error"] != nil {
			t.Fatalf("started payload: %v", started)
		}
		if rows := rigMessages(s, sid); rows[0]["checkpoint"] != nil {
			t.Fatalf("no checkpoint expected: %v", rows[0])
		}
		if list := s.ListTurnCheckpoints(sid); list["enabled"] != false {
			t.Fatalf("list: %v", list)
		}
	})
	t.Run("workspace cannot be scanned", func(t *testing.T) {
		s, sid, root := newCheckpointRig(t)
		s.rt.workspace = filepath.Join(root, "missing")
		started := runRigTurn(t, s, sid, "edit", "go")
		if started["checkpoint_id"] != nil || started["checkpoint_error"] == nil {
			t.Fatalf("the failure must be reported, not hidden: %v", started)
		}
		if rows := rigMessages(s, sid); len(rows) != 2 || rows[0]["checkpoint"] != nil {
			t.Fatalf("the turn itself must still run: %v", rows)
		}
	})
	t.Run("no session", func(t *testing.T) {
		s, _, _ := newCheckpointRig(t)
		started := runRigTurn(t, s, "", "edit", "go")
		if started["checkpoint_id"] != nil {
			t.Fatalf("a turn without a session has nothing to file the checkpoint under: %v", started)
		}
	})
}

func TestCheckpointsOfAnotherWorkspaceAreNotOffered(t *testing.T) {
	s, sid, _ := newCheckpointRig(t)
	id := runRigTurn(t, s, sid, "edit", "go")["checkpoint_id"].(string)
	s.rt.workspace = t.TempDir()

	if cps, _ := s.ListTurnCheckpoints(sid)["checkpoints"].([]workspace.CheckpointInfo); len(cps) != 0 {
		t.Fatalf("listed: %v", cps)
	}
	if out := s.RollbackToTurn(sid, id); out["success"] != false || !strings.Contains(out["error"].(string), "another workspace") {
		t.Fatalf("rollback: %v", out)
	}
}

func TestSwitchingWorkspaceDropsCheckpointsButReapplyingKeepsThem(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeRigFile(t, a, "f.txt", "x")
	r := &Runtime{dataDir: t.TempDir(), workspace: a, turnCheckpoints: newTurnCheckpointStore()}
	if _, err := r.turnCheckpoints.Begin("s", a, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetWorkspaceRoot(a); err != nil {
		t.Fatal(err)
	}
	if len(r.turnCheckpoints.List("s")) != 1 {
		t.Fatal("choosing the same folder again must keep the checkpoints")
	}
	if _, err := r.SetWorkspaceRoot(b); err != nil {
		t.Fatal(err)
	}
	if len(r.turnCheckpoints.List("s")) != 0 {
		t.Fatal("switching to another folder must drop them")
	}
}
