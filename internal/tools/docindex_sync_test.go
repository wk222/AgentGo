package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"agentgo/internal/docindex"
)

func (e *indexEnv) sync(restart bool, folder string) indexDocumentsOutput {
	e.t.Helper()
	out, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync", Paths: []string{folder}, Restart: restart})
	if err != nil {
		e.t.Fatalf("sync %s: %v", folder, err)
	}
	if out.Sync == nil {
		e.t.Fatalf("sync gave no outcome: %+v", out)
	}
	return out
}

func (e *indexEnv) indexed(folder string) map[string]bool {
	e.t.Helper()
	files, err := e.d.store.List(e.ctx, e.scope(), folder)
	if err != nil {
		e.t.Fatal(err)
	}
	m := map[string]bool{}
	for _, f := range files {
		m[f.Source] = true
	}
	return m
}

func TestWalkLessMatchesTheWalkOrder(t *testing.T) {
	// WalkDir enters directory "a" before it reaches file "a.txt", although "a.txt" < "a/b.txt" as plain strings.
	if !walkLess("a/b.txt", "a.txt") || walkLess("a.txt", "a/b.txt") {
		t.Fatal("a/b.txt must come before a.txt in walk order")
	}
	if walkLess("x/y", "x/y") || !walkLess("x", "x/y") {
		t.Fatal("equal paths are not less; a parent is before its children")
	}
}

func TestSyncAddsUpdatesAndDropsOnlyAfterACompleteWalk(t *testing.T) {
	e := newIndexEnv(t)
	var flagged []string
	e.d.onChange = func(_ context.Context, path, state string) (int, error) {
		flagged = append(flagged, state+":"+path)
		return 1, nil
	}
	e.write("docs/a.txt", []byte("alpha policy"))
	e.write("docs/sub/b.txt", []byte("beta policy"))
	first := e.sync(false, "docs")
	if first.Indexed != 2 || first.Sync.Status != docindex.SyncComplete || first.Sync.Dropped != 0 {
		t.Fatalf("first sync: %+v %+v", first, first.Sync)
	}

	e.write("docs/a.txt", []byte("alpha policy, revised in 2026"))
	e.write("docs/c.txt", []byte("gamma policy"))
	if err := os.Remove(filepath.Join(e.root, "docs", "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	second := e.sync(false, "docs")
	if second.Updated != 1 || second.Indexed != 1 || second.Sync.Dropped != 1 || second.Sync.Status != docindex.SyncComplete {
		t.Fatalf("second sync: %+v %+v", second, second.Sync)
	}
	if got := e.indexed("docs"); len(got) != 2 || !got["docs/a.txt"] || !got["docs/c.txt"] {
		t.Fatalf("index after sync: %v", got)
	}
	if len(flagged) != 2 || flagged[0] != "changed:docs/a.txt" || flagged[1] != "deleted:docs/sub/b.txt" {
		t.Fatalf("learned memories were told: %v", flagged)
	}
	third := e.sync(false, "docs")
	if third.Unchanged != 2 || third.Indexed+third.Updated+third.Sync.Dropped != 0 {
		t.Fatalf("a sync with nothing to do must do nothing: %+v %+v", third, third.Sync)
	}
}

func TestSyncWalksABigFolderInStepsAndNothingIsDroppedMidWalk(t *testing.T) {
	e := newIndexEnv(t)
	total := docIndexMaxFiles + 30
	for i := 0; i < total; i++ {
		e.write(fmt.Sprintf("big/f%03d.txt", i), []byte(fmt.Sprintf("document number %d", i)))
	}
	e.write("big/a/inner.txt", []byte("inside a")) // "a" is entered before f000.txt
	e.write("big/z.txt", []byte("last file"))

	step1 := e.sync(false, "big")
	if step1.Sync.Status != docindex.SyncRunning || !step1.Truncated || step1.Indexed != docIndexMaxFiles || step1.Sync.Cursor == "" {
		t.Fatalf("step 1: %+v %+v", step1, step1.Sync)
	}
	if !e.indexed("big")["big/a/inner.txt"] {
		t.Fatal("walk order: big/a/inner.txt belongs to the first step")
	}
	// Something already handled disappears while the walk is unfinished: no deletion decision may be taken yet.
	if err := os.Remove(filepath.Join(e.root, "big", "f000.txt")); err != nil {
		t.Fatal(err)
	}
	if !e.indexed("big")["big/f000.txt"] {
		t.Fatal("a file was dropped before the walk finished")
	}

	step2 := e.sync(false, "big")
	if step2.Sync.Status != docindex.SyncComplete || step2.Indexed != 32 {
		t.Fatalf("step 2: %+v %+v", step2.Sync, step2.Indexed)
	}
	// f000 was seen earlier in this same scan, so this scan keeps it; the next scan confirms it is gone.
	if !e.indexed("big")["big/f000.txt"] || !e.indexed("big")["big/z.txt"] {
		t.Fatal("a file seen during the scan was dropped by that scan, or the last file is missing")
	}
	step3 := e.sync(false, "big")
	if step3.Sync.Status != docindex.SyncComplete || step3.Sync.Dropped != 1 || e.indexed("big")["big/f000.txt"] {
		t.Fatalf("next scan should drop the deleted file: %+v", step3.Sync)
	}
	if got := len(e.indexed("big")); got != total+2-1 {
		t.Fatalf("indexed %d files, want %d", got, total+1)
	}
}

func TestSyncDoesNotDropAnythingWhenTheFolderIsGoneOrEmptied(t *testing.T) {
	e := newIndexEnv(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		e.write("share/"+n+".txt", []byte("note "+n))
	}
	if out := e.sync(false, "share"); out.Indexed != 4 {
		t.Fatalf("%+v", out)
	}

	// The folder is emptied (or an unmounted share shows up as an empty folder): held back, not dropped.
	for _, n := range []string{"a", "b", "c", "d"} {
		if err := os.Remove(filepath.Join(e.root, "share", n+".txt")); err != nil {
			t.Fatal(err)
		}
	}
	held := e.sync(false, "share")
	if held.Sync.Held == "" || held.Sync.Dropped != 0 || len(e.indexed("share")) != 4 {
		t.Fatalf("emptied folder must hold deletions back: %+v %v", held.Sync, e.indexed("share"))
	}

	// The folder itself is gone: the sync is refused and the index is untouched.
	if err := os.RemoveAll(filepath.Join(e.root, "share")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync", Paths: []string{"share"}}); err == nil {
		t.Fatal("syncing a missing folder must fail")
	}
	if len(e.indexed("share")) != 4 {
		t.Fatalf("index changed although the folder could not be read: %v", e.indexed("share"))
	}
}

func TestSyncCancelledMidWalkKeepsTheCursorAndDropsNothing(t *testing.T) {
	e := newIndexEnv(t)
	e.write("d/a.txt", []byte("aaa one"))
	e.write("d/b.txt", []byte("bbb two"))
	e.write("d/c.txt", []byte("ccc three"))
	e.sync(false, "d")
	if err := os.Remove(filepath.Join(e.root, "d", "b.txt")); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(e.ctx)
	cancel()
	if _, err := e.d.index(cancelled, indexDocumentsInput{Action: "sync", Paths: []string{"d"}}); err == nil {
		t.Fatal("a cancelled sync must not report success")
	}
	if len(e.indexed("d")) != 3 {
		t.Fatal("a cancelled sync dropped a file")
	}
	// The next call is a normal one and finishes the job.
	done := e.sync(false, "d")
	if done.Sync.Status != docindex.SyncComplete || done.Sync.Dropped != 1 || len(e.indexed("d")) != 2 {
		t.Fatalf("sync after cancellation: %+v", done.Sync)
	}
}

func TestSyncStateBelongsToTheWorkspaceAndTheScan(t *testing.T) {
	e := newIndexEnv(t)
	e.write("docs/a.txt", []byte("workspace one"))
	e.sync(false, "docs")
	scope1 := e.scope()
	root1 := e.root

	// Another workspace with a folder of the same name has its own index and its own scan.
	e.root = t.TempDir()
	e.write("docs/other.txt", []byte("workspace two"))
	if out := e.sync(false, "docs"); out.Indexed != 1 || out.Sync.Dropped != 0 {
		t.Fatalf("%+v %+v", out, out.Sync)
	}
	e.root = root1
	if got := e.indexed("docs"); len(got) != 1 || !got["docs/a.txt"] {
		t.Fatalf("workspace one's index was touched: %v", got)
	}
	if _, had, _ := e.d.store.SyncGet(e.ctx, scope1, "docs"); !had {
		t.Fatal("workspace one lost its scan state")
	}

	// A late call of a replaced scan can neither move the cursor nor add to what the new scan has seen.
	st := e.d.store
	old, err := st.SyncBegin(e.ctx, scope1, "late")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.SyncBegin(e.ctx, scope1, "late")
	if err != nil || fresh.ScanID == old.ScanID {
		t.Fatalf("a new scan needs a new id: %v", err)
	}
	if err := st.SyncProgress(e.ctx, scope1, "late", old.ScanID, "z.txt", []string{"late/z.txt"}, 1, false); !errors.Is(err, docindex.ErrStaleScan) {
		t.Fatalf("late progress was accepted: %v", err)
	}
	if err := st.SyncFinish(e.ctx, scope1, "late", old.ScanID, docindex.SyncComplete); !errors.Is(err, docindex.ErrStaleScan) {
		t.Fatalf("late finish was accepted: %v", err)
	}
	if cur, _, _ := st.SyncGet(e.ctx, scope1, "late"); cur.Cursor != "" || cur.Status != docindex.SyncRunning {
		t.Fatalf("the new scan was disturbed: %+v", cur)
	}
}

func TestSyncRestartStartsOverAndSyncNeedsExactlyOneFolder(t *testing.T) {
	e := newIndexEnv(t)
	e.write("d/a.txt", []byte("aaa"))
	first := e.sync(false, "d")
	again := e.sync(true, "d")
	if first.Sync.ScanID == again.Sync.ScanID {
		t.Fatal("restart must begin a new scan")
	}
	if _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync"}); err == nil {
		t.Fatal("sync without a folder must fail")
	}
	if _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync", Paths: []string{"a", "b"}}); err == nil {
		t.Fatal("sync of two folders in one call must fail")
	}
	if _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync", Paths: []string{"d/a.txt"}}); err == nil {
		t.Fatal("sync of a file must fail")
	}
}
