package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func cpWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cpRead(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func cpGone(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return errors.Is(err, fs.ErrNotExist)
}

func cpRemove(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

func cpOf(st *CheckpointStore, sessionID, id string) *checkpoint {
	st.mu.Lock()
	defer st.mu.Unlock()
	if sc := st.sessions[reviewSessionID(sessionID)]; sc != nil {
		return sc.find(id)
	}
	return nil
}

// assertStoreConsistent checks the bookkeeping invariants: every kept blob is
// referenced exactly as often as the manifests say, and the byte counters add up.
func assertStoreConsistent(t *testing.T, st *CheckpointStore) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	var total int64
	for sid, sc := range st.sessions {
		refs := make(map[blobKey]int)
		for _, cp := range sc.list {
			var ref int64
			seen := make(map[blobKey]bool)
			for path, f := range cp.files {
				if !f.Restorable {
					continue
				}
				b := sc.blobs[f.Hash]
				if b == nil {
					t.Errorf("session %s checkpoint %s: %s is marked restorable but its blob is gone", sid, cp.id, path)
					continue
				}
				refs[f.Hash]++
				if !seen[f.Hash] {
					seen[f.Hash] = true
					ref += int64(len(b.data))
				}
			}
			if ref != cp.refBytes {
				t.Errorf("session %s checkpoint %s: refBytes %d, recomputed %d", sid, cp.id, cp.refBytes, ref)
			}
		}
		var bytes int64
		for h, b := range sc.blobs {
			if b.refs != refs[h] {
				t.Errorf("session %s: blob has refs=%d but manifests reference it %d times", sid, b.refs, refs[h])
			}
			if refs[h] == 0 {
				t.Errorf("session %s: orphan blob of %d bytes", sid, len(b.data))
			}
			bytes += int64(len(b.data))
		}
		if bytes != sc.bytes {
			t.Errorf("session %s: bytes counter %d, blobs add up to %d", sid, sc.bytes, bytes)
		}
		total += sc.bytes
	}
	if total != st.total {
		t.Errorf("global counter %d, sessions add up to %d", st.total, total)
	}
}

func TestCheckpointRollbackRestoresModifiedDeletedAndCreated(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "alpha")
	cpWrite(t, root, "b.txt", "bravo")
	cpWrite(t, root, "dir/sub/d.txt", "delta")
	st := NewCheckpointStore(CheckpointLimits{})

	cp, err := st.Begin("s1", root, "  fix\nthe   bug ")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Files != 3 || cp.Kind != CheckpointTurn || cp.Prompt != "fix the bug" || cp.Seq != 1 {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}

	cpWrite(t, root, "a.txt", "alpha CHANGED")
	cpWrite(t, root, "dir/sub/d.txt", "delta2")
	cpRemove(t, root, "b.txt")
	cpWrite(t, root, "c.txt", "charlie")
	cpWrite(t, root, "dir/new/e.txt", "echo")

	preview, err := st.Preview("s1", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Restore != 2 || preview.Recreate != 1 || preview.Delete != 2 || preview.Skipped != 0 {
		t.Fatalf("preview: %+v", preview)
	}

	res, err := st.Rollback("s1", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 2 || res.Recreated != 1 || res.Deleted != 2 || len(res.Failed) != 0 || len(res.Skipped) != 0 || res.UndoID == "" {
		t.Fatalf("result: %+v", res)
	}
	if cpRead(t, root, "a.txt") != "alpha" || cpRead(t, root, "b.txt") != "bravo" || cpRead(t, root, "dir/sub/d.txt") != "delta" {
		t.Fatal("files were not put back")
	}
	if !cpGone(root, "c.txt") || !cpGone(root, "dir/new/e.txt") {
		t.Fatal("files created after the checkpoint should be removed")
	}
	if _, err := os.Stat(filepath.Join(root, "dir", "new")); err != nil {
		t.Fatalf("folders must survive a rollback: %v", err)
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".agentgo-restore-") {
			t.Errorf("temp file left behind: %s", p)
		}
		return nil
	})

	// The automatic checkpoint taken before the rollback undoes it.
	undo, err := st.Rollback("s1", root, res.UndoID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Restored != 2 || undo.Recreated != 2 || undo.Deleted != 1 || len(undo.Failed) != 0 {
		t.Fatalf("undo result: %+v", undo)
	}
	if cpRead(t, root, "a.txt") != "alpha CHANGED" || cpRead(t, root, "dir/sub/d.txt") != "delta2" ||
		cpRead(t, root, "c.txt") != "charlie" || cpRead(t, root, "dir/new/e.txt") != "echo" || !cpGone(root, "b.txt") {
		t.Fatal("undo did not bring back the state before the rollback")
	}

	var kinds []string
	for _, info := range st.List("s1") {
		kinds = append(kinds, info.Kind)
	}
	if got := strings.Join(kinds, ","); got != "turn,pre_rollback,pre_rollback" {
		t.Fatalf("kinds = %s", got)
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointPerTurnRollbackSharesBlobs(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "keep.txt", strings.Repeat("k", 1000))
	cpWrite(t, root, "work.txt", "v0")
	st := NewCheckpointStore(CheckpointLimits{})

	t1, err := st.Begin("s", root, "turn 1")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "work.txt", "v1")
	cpWrite(t, root, "t1.txt", "made in turn 1")
	t2, err := st.Begin("s", root, "turn 2")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "work.txt", "v2")
	cpWrite(t, root, "t2.txt", "made in turn 2!")
	t3, err := st.Begin("s", root, "turn 3")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "work.txt", "v3")

	// keep.txt is stored once, not once per turn.
	want := int64(1000 + len("v0") + len("v1") + len("v2") + len("made in turn 1") + len("made in turn 2!"))
	if got := st.bytesHeld("s"); got != want {
		t.Fatalf("bytes held = %d, want %d", got, want)
	}
	assertStoreConsistent(t, st)

	if _, err := st.Rollback("s", root, t2.ID); err != nil {
		t.Fatal(err)
	}
	if cpRead(t, root, "work.txt") != "v1" || cpRead(t, root, "t1.txt") != "made in turn 1" || !cpGone(root, "t2.txt") {
		t.Fatal("state of turn 2 not restored")
	}
	if _, err := st.Rollback("s", root, t1.ID); err != nil {
		t.Fatal(err)
	}
	if cpRead(t, root, "work.txt") != "v0" || !cpGone(root, "t1.txt") || !cpGone(root, "t2.txt") {
		t.Fatal("state of turn 1 not restored")
	}
	// Going forward again works too: every turn keeps its own checkpoint.
	if _, err := st.Rollback("s", root, t3.ID); err != nil {
		t.Fatal(err)
	}
	if cpRead(t, root, "work.txt") != "v2" || cpRead(t, root, "t1.txt") != "made in turn 1" || cpRead(t, root, "t2.txt") != "made in turn 2!" {
		t.Fatal("state of turn 3 not restored")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointPreviewChangesNothing(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "one")
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "a.txt", "two")
	cpWrite(t, root, "new.txt", "new")
	before := len(st.List("s"))
	held := st.bytesHeld("s")

	if _, err := st.Preview("s", root, cp.ID); err != nil {
		t.Fatal(err)
	}
	if cpRead(t, root, "a.txt") != "two" || cpRead(t, root, "new.txt") != "new" {
		t.Fatal("a preview must not touch the workspace")
	}
	if len(st.List("s")) != before || st.bytesHeld("s") != held {
		t.Fatal("a preview must not add checkpoints")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointSkipsWhatItCannotRestoreOrBackUp(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("B", 100)
	cpWrite(t, root, "big.txt", big)  // too big to keep, changed later
	cpWrite(t, root, "big2.txt", big) // too big to keep, deleted later
	cpWrite(t, root, "small.txt", "hi")
	cpWrite(t, root, "grow.txt", "tiny") // small now, huge later
	st := NewCheckpointStore(CheckpointLimits{MaxFileBytes: 16})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}

	cpWrite(t, root, "big.txt", strings.Repeat("X", 100))
	cpRemove(t, root, "big2.txt")
	cpWrite(t, root, "small.txt", "changed")
	cpWrite(t, root, "grow.txt", strings.Repeat("G", 100))
	cpWrite(t, root, "newbig.txt", big)
	cpWrite(t, root, "newsmall.txt", "x")

	preview, err := st.Preview("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Restore != 1 || preview.Delete != 1 || preview.Recreate != 0 || preview.Skipped != 4 {
		t.Fatalf("preview: %+v", preview)
	}

	res, err := st.Rollback("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 || res.Deleted != 1 || res.Recreated != 0 || len(res.Skipped) != 4 || len(res.Failed) != 0 {
		t.Fatalf("result: %+v", res)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Path] = s.Reason
	}
	for path, want := range map[string]string{
		"big.txt": reasonNotStored, "big2.txt": reasonNotStored, "grow.txt": reasonNoBackup, "newbig.txt": reasonNoBackup,
	} {
		if reasons[path] != want {
			t.Errorf("%s: reason %q, want %q", path, reasons[path], want)
		}
	}
	if cpRead(t, root, "small.txt") != "hi" || !cpGone(root, "newsmall.txt") {
		t.Fatal("what can be undone should still be rolled back")
	}
	if cpRead(t, root, "big.txt") != strings.Repeat("X", 100) || !cpGone(root, "big2.txt") ||
		cpRead(t, root, "grow.txt") != strings.Repeat("G", 100) || cpRead(t, root, "newbig.txt") != big {
		t.Fatal("skipped files must stay exactly as they were")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointNothingToDoLeavesNoSafetyCheckpoint(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "same")
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Rollback("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.UndoID != "" || res.Restored+res.Recreated+res.Deleted != 0 || len(st.List("s")) != 1 {
		t.Fatalf("result %+v, checkpoints %d", res, len(st.List("s")))
	}
}

func TestCheckpointRejectsWrongRootUnknownIDAndOtherSession(t *testing.T) {
	root1, root2 := t.TempDir(), t.TempDir()
	cpWrite(t, root1, "a.txt", "a")
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rollback("s", root2, cp.ID); !errors.Is(err, ErrCheckpointRoot) {
		t.Fatalf("other root: %v", err)
	}
	if _, err := st.Preview("s", root2, cp.ID); !errors.Is(err, ErrCheckpointRoot) {
		t.Fatalf("other root preview: %v", err)
	}
	if _, err := st.Rollback("s", root1, "cp_nope"); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := st.Rollback("someone-else", root1, cp.ID); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("other session: %v", err)
	}
	if _, err := st.Begin("s", filepath.Join(root1, "missing"), ""); err == nil {
		t.Fatal("a missing root must be an error")
	}
	if len(st.List("s")) != 1 {
		t.Fatal("a failed Begin must not leave a checkpoint")
	}
}

func TestCheckpointRecyclesOldestPerSession(t *testing.T) {
	root := t.TempDir()
	st := NewCheckpointStore(CheckpointLimits{MaxPerSession: 3})
	var ids []string
	for i := 0; i < 5; i++ {
		cpWrite(t, root, "f.txt", fmt.Sprintf("v%d", i))
		cp, err := st.Begin("s", root, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cp.ID)
	}
	list := st.List("s")
	if len(list) != 3 || list[0].ID != ids[2] || list[0].Seq != 3 {
		t.Fatalf("list: %+v", list)
	}
	if _, err := st.Preview("s", root, ids[0]); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("recycled checkpoint: %v", err)
	}
	if got := st.bytesHeld("s"); got != 6 { // v2, v3, v4
		t.Fatalf("bytes held = %d, want 6", got)
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointStaysWithinTheSessionBudget(t *testing.T) {
	v := func(c byte) string { return strings.Repeat(string(c), 100) }

	t.Run("old checkpoints are recycled", func(t *testing.T) {
		root := t.TempDir()
		st := NewCheckpointStore(CheckpointLimits{MaxSessionBytes: 150})
		cpWrite(t, root, "f.txt", v('1'))
		first, err := st.Begin("s", root, "")
		if err != nil {
			t.Fatal(err)
		}
		cpWrite(t, root, "f.txt", v('2'))
		second, err := st.Begin("s", root, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := st.Get("s", first.ID); ok {
			t.Fatal("the oldest checkpoint should be recycled to fit 150 bytes")
		}
		if _, ok := st.Get("s", second.ID); !ok || st.bytesHeld("s") != 100 {
			t.Fatalf("newest must stay; held %d", st.bytesHeld("s"))
		}
		if _, err := st.Rollback("s", root, first.ID); !errors.Is(err, ErrCheckpointNotFound) {
			t.Fatalf("recycled id: %v", err)
		}
		assertStoreConsistent(t, st)
	})

	t.Run("one scan over budget keeps hashes only", func(t *testing.T) {
		root := t.TempDir()
		st := NewCheckpointStore(CheckpointLimits{MaxSessionBytes: 150})
		cpWrite(t, root, "a.txt", v('a'))
		cpWrite(t, root, "b.txt", v('b'))
		cp, err := st.Begin("s", root, "")
		if err != nil {
			t.Fatal(err)
		}
		if cp.Skipped != 1 || st.bytesHeld("s") != 100 {
			t.Fatalf("skipped %d, held %d", cp.Skipped, st.bytesHeld("s"))
		}
		cpWrite(t, root, "a.txt", "changed a")
		cpWrite(t, root, "b.txt", "changed b")
		res, err := st.Rollback("s", root, cp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Restored != 1 || len(res.Skipped) != 1 {
			t.Fatalf("result: %+v", res)
		}
		assertStoreConsistent(t, st)
	})

	t.Run("blobs of earlier turns count towards one checkpoint", func(t *testing.T) {
		root := t.TempDir()
		st := NewCheckpointStore(CheckpointLimits{MaxSessionBytes: 150})
		cpWrite(t, root, "a.txt", v('a'))
		if _, err := st.Begin("s", root, ""); err != nil {
			t.Fatal(err)
		}
		cpWrite(t, root, "b.txt", v('b'))
		second, err := st.Begin("s", root, "")
		if err != nil {
			t.Fatal(err)
		}
		// a.txt (already stored) + b.txt (new) would be 200 bytes: the new one gives way.
		if second.Skipped != 1 || st.bytesHeld("s") != 100 {
			t.Fatalf("skipped %d, held %d", second.Skipped, st.bytesHeld("s"))
		}
		assertStoreConsistent(t, st)
	})
}

func TestCheckpointRefusesToWriteThroughALinkOutsideTheWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	cpWrite(t, root, "docs/note.txt", "original")
	cpWrite(t, root, "plain.txt", "p1")
	cpWrite(t, outside, "note.txt", "OUTSIDE")
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}

	// docs/ becomes a link to a folder outside the workspace.
	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	linkDirForTest(t, filepath.Join(root, "docs"), outside)
	cpWrite(t, root, "plain.txt", "p2")

	res, err := st.Rollback("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "note.txt")); string(got) != "OUTSIDE" {
		t.Fatalf("a file outside the workspace was overwritten: %q", got)
	}
	if len(res.Failed) != 1 || res.Failed[0].Path != "docs/note.txt" || res.Recreated != 0 {
		t.Fatalf("result: %+v", res)
	}
	if cpRead(t, root, "plain.txt") != "p1" || res.Restored != 1 {
		t.Fatal("the rest of the rollback must still happen")
	}
}

func TestCheckpointScanLimits(t *testing.T) {
	t.Run("timeout keeps nothing", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 800; i++ {
			cpWrite(t, root, fmt.Sprintf("d%d/f%d.txt", i%20, i), "content")
		}
		st := NewCheckpointStore(CheckpointLimits{ScanTimeout: time.Millisecond})
		if _, err := st.Begin("s", root, ""); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
		if len(st.List("s")) != 0 || st.bytesHeld("s") != 0 {
			t.Fatal("a scan that timed out must leave nothing behind")
		}
		assertStoreConsistent(t, st)
	})
	t.Run("too many files", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 5; i++ {
			cpWrite(t, root, fmt.Sprintf("f%d.txt", i), "x")
		}
		st := NewCheckpointStore(CheckpointLimits{MaxFiles: 3})
		if _, err := st.Begin("s", root, ""); !errors.Is(err, errTooManyFiles) {
			t.Fatalf("err = %v", err)
		}
		if len(st.List("s")) != 0 {
			t.Fatal("no checkpoint expected")
		}
	})
}

func TestCheckpointPausesAfterAScanThatFailedForSize(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		cpWrite(t, root, fmt.Sprintf("f%d.txt", i), "x")
	}
	st := NewCheckpointStore(CheckpointLimits{MaxFiles: 3})
	if _, err := st.Begin("s", root, ""); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("first attempt: %v", err)
	}
	// A turn must not wait for a scan that is known to fail.
	if _, err := st.Begin("s", root, ""); !errors.Is(err, ErrCheckpointPaused) {
		t.Fatalf("second attempt: %v", err)
	}
	other := t.TempDir()
	cpWrite(t, other, "a.txt", "a")
	if _, err := st.Begin("s", other, ""); err != nil {
		t.Fatalf("the pause is per workspace: %v", err)
	}
	st.mu.Lock()
	st.paused[rootKey(root)] = time.Now().Add(-time.Second)
	st.mu.Unlock()
	if _, err := st.Begin("s", root, ""); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("once the pause is over it scans again: %v", err)
	}
	st.Clear()
	if _, err := st.Begin("s", root, ""); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("Clear must lift the pause: %v", err)
	}
}

func TestCheckpointNeverTouchesIgnoredFolders(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "src/a.go", "package a")
	cpWrite(t, root, "node_modules/x/index.js", "orig")
	cpWrite(t, root, ".git/HEAD", "ref: main")
	cpWrite(t, root, "dist/out.js", "orig")
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Files != 1 {
		t.Fatalf("only src/a.go is tracked, got %d files", cp.Files)
	}

	cpWrite(t, root, "src/a.go", "package b")
	cpWrite(t, root, "node_modules/x/index.js", "changed")
	cpWrite(t, root, "node_modules/new.js", "new")
	cpWrite(t, root, ".git/HEAD", "ref: other")
	cpWrite(t, root, "dist/new.js", "new")
	cpRemove(t, root, "dist/out.js")

	res, err := st.Rollback("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 || res.Deleted != 0 || res.Recreated != 0 {
		t.Fatalf("result: %+v", res)
	}
	if cpRead(t, root, "src/a.go") != "package a" || cpRead(t, root, "node_modules/x/index.js") != "changed" ||
		cpRead(t, root, "node_modules/new.js") != "new" || cpRead(t, root, ".git/HEAD") != "ref: other" ||
		cpRead(t, root, "dist/new.js") != "new" || !cpGone(root, "dist/out.js") {
		t.Fatal("ignored folders must be left exactly as they are")
	}
}

func TestCheckpointRereadsFilesRewrittenWithinTheTimestampWindow(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "aaaa")
	full := filepath.Join(root, "a.txt")
	st := NewCheckpointStore(CheckpointLimits{})
	if _, err := st.Begin("s", root, "one"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	// Same size and same mtime: size+mtime alone cannot tell these two apart.
	cpWrite(t, root, "a.txt", "bbbb")
	if err := os.Chtimes(full, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := st.Begin("s", root, "two")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "a.txt", "cccc")
	if _, err := st.Rollback("s", root, second.ID); err != nil {
		t.Fatal(err)
	}
	if got := cpRead(t, root, "a.txt"); got != "bbbb" {
		t.Fatalf("restored %q, want the content at the start of turn two", got)
	}
}

// An unchanged size and mtime outside the racy window means "not modified": such a
// file is not read again. That is what keeps a turn's checkpoint a cheap stat pass,
// so the contract is pinned here (a rewrite that forges both is invisible, as it is to git).
func TestCheckpointReusesEntriesOfFilesWithUnchangedStat(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "aaaa")
	full := filepath.Join(root, "a.txt")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(full, old, old); err != nil {
		t.Fatal(err)
	}
	st := NewCheckpointStore(CheckpointLimits{})
	first, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	cpWrite(t, root, "a.txt", "bbbb")
	if err := os.Chtimes(full, old, old); err != nil {
		t.Fatal(err)
	}
	second, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	a, b := cpOf(st, "s", first.ID), cpOf(st, "s", second.ID)
	if a.files["a.txt"].Hash != b.files["a.txt"].Hash {
		t.Fatal("a file with an unchanged stat should reuse the previous entry")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointLeavesUnreadableFilesAlone(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "ok.txt", "ok1")
	cpWrite(t, root, "locked.txt", "L1")
	release := lockFileForTest(t, filepath.Join(root, "locked.txt"))
	st := NewCheckpointStore(CheckpointLimits{})
	cp, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Files != 2 || cp.Skipped < 1 {
		t.Fatalf("the unreadable file must still be listed: %+v", cp)
	}
	release()

	cpWrite(t, root, "locked.txt", "L2")
	cpWrite(t, root, "ok.txt", "ok2")
	preview, err := st.Preview("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Restore != 1 || preview.Unverified != 1 || preview.Delete != 0 {
		t.Fatalf("preview: %+v", preview)
	}
	res, err := st.Rollback("s", root, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 || res.Deleted != 0 || cpRead(t, root, "ok.txt") != "ok1" || cpRead(t, root, "locked.txt") != "L2" {
		t.Fatalf("result %+v, locked.txt = %q", res, cpRead(t, root, "locked.txt"))
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointPinnedTargetSurvivesRecycling(t *testing.T) {
	root := t.TempDir()
	st := NewCheckpointStore(CheckpointLimits{MaxPerSession: 2})
	cpWrite(t, root, "f.txt", "v-1")
	first, err := st.Begin("s", root, "")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := st.pin("s", root, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		cpWrite(t, root, "f.txt", fmt.Sprintf("v%d", i))
		if _, err := st.Begin("s", root, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := st.Get("s", first.ID); !ok {
		t.Fatal("a checkpoint in use by a rollback must not be recycled")
	}
	st.unpin(pinned)
	cpWrite(t, root, "f.txt", "v-last")
	if _, err := st.Begin("s", root, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("s", first.ID); ok {
		t.Fatal("once released it can be recycled")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointGlobalBudgetRecyclesIdleSessions(t *testing.T) {
	root := t.TempDir()
	st := NewCheckpointStore(CheckpointLimits{MaxGlobalBytes: 150})
	cpWrite(t, root, "f.txt", strings.Repeat("1", 100))
	idle, err := st.Begin("idle", root, "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	cpWrite(t, root, "f.txt", strings.Repeat("2", 100))
	busy, err := st.Begin("busy", root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("idle", idle.ID); ok {
		t.Fatal("the least recently used session should give way")
	}
	if _, ok := st.Get("busy", busy.ID); !ok {
		t.Fatal("the session that just took a checkpoint keeps it")
	}
	assertStoreConsistent(t, st)
}

func TestCheckpointDropAndClear(t *testing.T) {
	root := t.TempDir()
	cpWrite(t, root, "a.txt", "aaa")
	st := NewCheckpointStore(CheckpointLimits{})
	if _, err := st.Begin("s1", root, ""); err != nil {
		t.Fatal(err)
	}
	keep, err := st.Begin("s2", root, "")
	if err != nil {
		t.Fatal(err)
	}
	st.Drop("s1")
	if len(st.List("s1")) != 0 || st.bytesHeld("s1") != 0 {
		t.Fatal("Drop must forget the session")
	}
	if _, ok := st.Get("s2", keep.ID); !ok {
		t.Fatal("other sessions are untouched")
	}
	assertStoreConsistent(t, st)
	st.Clear()
	if len(st.List("s2")) != 0 || st.total != 0 {
		t.Fatal("Clear must forget everything")
	}
}

func TestCheckpointConcurrentBeginKeepsAccountingConsistent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		cpWrite(t, root, fmt.Sprintf("shared/f%d.txt", i), fmt.Sprintf("shared content %d", i))
	}
	st := NewCheckpointStore(CheckpointLimits{MaxPerSession: 5})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				name := filepath.Join(root, fmt.Sprintf("own%d.txt", g))
				if err := os.WriteFile(name, []byte(fmt.Sprintf("g%d-%d", g, i)), 0o644); err != nil {
					errs <- err
					return
				}
				if _, err := st.Begin("s", root, ""); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := len(st.List("s")); n == 0 || n > 5 {
		t.Fatalf("%d checkpoints kept, limit is 5", n)
	}
	assertStoreConsistent(t, st)
}
