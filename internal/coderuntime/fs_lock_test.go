package coderuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPathLocksSerialiseOneKeyButNotTwo(t *testing.T) {
	var l pathLocks
	ctx := context.Background()

	relA, err := l.acquire(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	relB, err := l.acquire(ctx, "b") // another key must not wait for "a"
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan struct{})
	go func() {
		rel, err := l.acquire(ctx, "a")
		if err == nil {
			close(got)
			rel()
		}
	}()
	select {
	case <-got:
		t.Fatal("a second holder got the lock for a key that was still held")
	case <-time.After(50 * time.Millisecond):
	}
	relA()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken after the holder released")
	}
	relB()
}

func TestPathLocksWaiterGivesUpWhenContextEnds(t *testing.T) {
	var l pathLocks
	rel, err := l.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.acquire(ctx, "a")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiter ignored its cancelled context")
	}

	rel()
	if n := l.size(); n != 0 {
		t.Fatalf("%d lock entries left after the holder and the cancelled waiter were done", n)
	}
}

func TestPathLocksDropIdleEntries(t *testing.T) {
	var l pathLocks
	for i := 0; i < 50; i++ {
		rel, err := l.acquire(context.Background(), fmt.Sprintf("f%d", i))
		if err != nil {
			t.Fatal(err)
		}
		rel()
		rel() // releasing twice must be harmless
	}
	if n := l.size(); n != 0 {
		t.Fatalf("%d idle lock entries kept", n)
	}
}

func fsTool(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no %q tool", name)
	return Tool{}
}

func fsArgs(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// viewed returns the tools with every file in paths already viewed in session
// "s", which the edit tools demand.
func viewed(t *testing.T, opts FSOptions, paths ...string) []Tool {
	t.Helper()
	tools := NewFSTools(opts)
	view := fsTool(t, tools, "view")
	for _, p := range paths {
		if r, err := view.Run(context.Background(), "s", "v", fsArgs(map[string]any{"file_path": p})); err != nil || r.IsError {
			t.Fatalf("view %s: %v %+v", p, err, r)
		}
	}
	return tools
}

// runBatch fires the way a model's single reply does: three calls on one file
// at the same moment. It returns how many of them reported an error and which
// of the four changes are missing from the final file.
func runBatch(t *testing.T, opts FSOptions, dir string, n int) (errs int, missing []string) {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("f%d.txt", n))
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := viewed(t, opts, p)
	edit, multi := fsTool(t, tools, "edit"), fsTool(t, tools, "multiedit")
	ctx := context.Background()

	calls := []func() (ToolResult, error){
		func() (ToolResult, error) {
			return edit.Run(ctx, "s", "c1", fsArgs(map[string]any{"file_path": p, "old_string": "alpha", "new_string": "ALPHA"}))
		},
		func() (ToolResult, error) {
			return edit.Run(ctx, "s", "c2", fsArgs(map[string]any{"file_path": p, "old_string": "beta", "new_string": "BETA"}))
		},
		func() (ToolResult, error) {
			return multi.Run(ctx, "s", "c3", fsArgs(map[string]any{"file_path": p, "edits": []map[string]any{
				{"old_string": "gamma", "new_string": "GAMMA"},
				{"old_string": "delta", "new_string": "DELTA"},
			}}))
		},
	}
	var wg sync.WaitGroup
	var failed atomic.Int32
	for _, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := call(); err != nil || r.IsError {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ALPHA", "BETA", "GAMMA", "DELTA"} {
		if !strings.Contains(string(b), want) {
			missing = append(missing, want)
		}
	}
	return int(failed.Load()), missing
}

// Before the per-path lock, two of these calls could read the same bytes and the
// later write dropped the earlier change while both reported success. The probe
// that found this lost one of two edits in 8 of 300 rounds with no approval step
// and in every round once approval took a few milliseconds.
func TestConcurrentEditsOnOneFileAllLand(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 150; i++ {
		if errs, missing := runBatch(t, FSOptions{WorkDir: dir}, dir, i); errs != 0 || len(missing) != 0 {
			t.Fatalf("round %d: %d call(s) failed, changes missing from the file: %v", i, errs, missing)
		}
	}
}

func TestConcurrentEditsWaitingForApprovalAllLand(t *testing.T) {
	dir := t.TempDir()
	opts := FSOptions{WorkDir: dir, Approve: func(context.Context, FSRequest) bool {
		time.Sleep(2 * time.Millisecond)
		return true
	}}
	for i := 0; i < 25; i++ {
		if errs, missing := runBatch(t, opts, dir, i); errs != 0 || len(missing) != 0 {
			t.Fatalf("round %d: %d call(s) failed, changes missing from the file: %v", i, errs, missing)
		}
	}
}

// The second call must not ask for approval while the first is still pending:
// it has to read the file the first one leaves behind before it can say what it
// would change.
func TestApprovalsForOneFileNeverOverlap(t *testing.T) {
	dir := t.TempDir()
	var inFlight, widest atomic.Int32
	opts := FSOptions{WorkDir: dir, Approve: func(context.Context, FSRequest) bool {
		n := inFlight.Add(1)
		for {
			w := widest.Load()
			if n <= w || widest.CompareAndSwap(w, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return true
	}}
	if errs, missing := runBatch(t, opts, dir, 0); errs != 0 || len(missing) != 0 {
		t.Fatalf("%d call(s) failed, changes missing: %v", errs, missing)
	}
	if w := widest.Load(); w != 1 {
		t.Fatalf("%d approvals for one file were pending at once, want 1", w)
	}
}

// Locking is per file: edits to different files must still be in flight together.
func TestEditsOnDifferentFilesStillRunTogether(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var pending atomic.Int32
	bothPending := make(chan struct{})
	var once sync.Once
	opts := FSOptions{WorkDir: dir, Approve: func(ctx context.Context, _ FSRequest) bool {
		if pending.Add(1) == 2 {
			once.Do(func() { close(bothPending) })
		}
		select {
		case <-bothPending:
			return true
		case <-time.After(2 * time.Second):
			return false // the other call never got as far as asking
		}
	}}
	tools := viewed(t, opts, a, b)
	edit := fsTool(t, tools, "edit")

	var wg sync.WaitGroup
	var denied atomic.Int32
	for _, p := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := edit.Run(context.Background(), "s", "c", fsArgs(map[string]any{"file_path": p, "old_string": "one", "new_string": "two"}))
			if err != nil || r.IsError {
				denied.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := denied.Load(); n != 0 {
		t.Fatalf("%d edit(s) to different files did not run together, so the lock is not per file", n)
	}
}

// A call queued behind an edit that waits for a person must give up when its run
// is cancelled instead of hanging for as long as that approval stays open.
func TestQueuedEditReturnsWhenItsRunIsCancelled(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("one two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	holding := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	opts := FSOptions{WorkDir: dir, Approve: func(ctx context.Context, _ FSRequest) bool {
		if first.CompareAndSwap(false, true) {
			close(holding)
			<-release
		}
		return true
	}}
	tools := viewed(t, opts, p)
	edit := fsTool(t, tools, "edit")

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		edit.Run(context.Background(), "s", "c1", fsArgs(map[string]any{"file_path": p, "old_string": "one", "new_string": "1"}))
	}()
	<-holding

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := edit.Run(ctx, "s", "c2", fsArgs(map[string]any{"file_path": p, "old_string": "two", "new_string": "2"}))
		queued <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued edit returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a queued edit stayed blocked after its context was cancelled")
	}

	close(release)
	<-firstDone
}
