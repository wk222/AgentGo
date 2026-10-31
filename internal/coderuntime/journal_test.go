package coderuntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func journalTools(t *testing.T) (tools []Tool, dir string, j *Journal) {
	t.Helper()
	dir = t.TempDir()
	j = OpenJournal(dir)
	return NewFSTools(FSOptions{WorkDir: dir, Journal: j, DisableBackup: true}), dir, j
}

func runTool(t *testing.T, tools []Tool, name, session, call string, args map[string]any) ToolResult {
	t.Helper()
	r, err := fsTool(t, tools, name).Run(context.Background(), session, call, fsArgs(args))
	if err != nil || r.IsError {
		t.Fatalf("%s: %v %+v", name, err, r)
	}
	return r
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// One reply creates a file, edits another and rewrites a third; undoing the latest
// call puts exactly that call's files back and leaves the earlier call alone.
func TestRevertLatestCallPutsBackOnlyThatCall(t *testing.T) {
	tools, dir, _ := journalTools(t)
	a, b, c := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt"), filepath.Join(dir, "c.txt")
	if err := os.WriteFile(b, []byte("old b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("old c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTool(t, tools, "view", "s", "v1", map[string]any{"file_path": b})
	runTool(t, tools, "view", "s", "v2", map[string]any{"file_path": c})

	runTool(t, tools, "edit", "s", "call-1", map[string]any{"file_path": b, "old_string": "old", "new_string": "first"})
	runTool(t, tools, "write", "s", "call-2", map[string]any{"file_path": a, "content": "new a\n"})
	runTool(t, tools, "write", "s", "call-2", map[string]any{"file_path": c, "content": "rewritten c\n"})

	res := runTool(t, tools, "revert_file_changes", "s", "u1", map[string]any{})
	if !strings.Contains(res.Content, a) || !strings.Contains(res.Content, c) {
		t.Fatalf("summary: %s", res.Content)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatalf("the file call-2 created must be gone: %v", err)
	}
	if got := read(t, c); got != "old c\n" {
		t.Fatalf("c = %q, want the version before call-2", got)
	}
	if got := read(t, b); got != "first b\n" {
		t.Fatalf("b = %q: call-1 must be untouched by undoing call-2", got)
	}
	// Reverting again reaches call-1; after that the session has nothing left to undo.
	runTool(t, tools, "revert_file_changes", "s", "u2", map[string]any{})
	if got := read(t, b); got != "old b\n" {
		t.Fatalf("b = %q after the second undo", got)
	}
	r, _ := fsTool(t, tools, "revert_file_changes").Run(context.Background(), "s", "u3", "{}")
	if !r.IsError || !strings.Contains(r.Content, "nothing to revert") {
		t.Fatalf("third undo: %+v", r)
	}
}

// Going back to before call N undoes N and everything after it, including several
// changes to the same file, in the right order.
func TestRevertFromACallRollsBackLaterChangesOfTheSameFile(t *testing.T) {
	tools, dir, _ := journalTools(t)
	p := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(p, []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTool(t, tools, "view", "s", "v", map[string]any{"file_path": p})
	for i, step := range [][2]string{{"v0", "v1"}, {"v1", "v2"}, {"v2", "v3"}} {
		runTool(t, tools, "edit", "s", "c"+string(rune('1'+i)), map[string]any{"file_path": p, "old_string": step[0], "new_string": step[1]})
	}
	runTool(t, tools, "revert_file_changes", "s", "u", map[string]any{"call_id": "c2"})
	if got := read(t, p); got != "v1\n" {
		t.Fatalf("doc = %q, want the state after c1", got)
	}
	runTool(t, tools, "revert_file_changes", "s", "u", map[string]any{"call_id": "all"})
	if got := read(t, p); got != "v0\n" {
		t.Fatalf("doc = %q, want the original", got)
	}
}

// Work the user did after the agent's change is never overwritten.
func TestRevertLeavesAFileTheUserChangedAfterwards(t *testing.T) {
	tools, dir, j := journalTools(t)
	p := filepath.Join(dir, "mine.txt")
	runTool(t, tools, "write", "s", "c1", map[string]any{"file_path": p, "content": "agent version\n"})
	if err := os.WriteFile(p, []byte("agent version\nplus my own work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runTool(t, tools, "revert_file_changes", "s", "u", map[string]any{})
	if !strings.Contains(res.Content, "Left alone") || !strings.Contains(res.Content, p) {
		t.Fatalf("summary: %s", res.Content)
	}
	if got := read(t, p); !strings.Contains(got, "my own work") {
		t.Fatalf("the user's work was lost: %q", got)
	}
	if cs := j.List("s"); len(cs) != 1 || cs[0].Reverted {
		t.Fatalf("a conflict must stay undoable once the user decides: %+v", cs)
	}
}

// A different session sees only its own changes, and the journal survives a new
// process (it is on disk, not in memory).
func TestJournalIsPerSessionAndPersistent(t *testing.T) {
	tools, dir, _ := journalTools(t)
	p := filepath.Join(dir, "x.txt")
	runTool(t, tools, "write", "s1", "c1", map[string]any{"file_path": p, "content": "x\n"})
	if cs := OpenJournal(dir).List("s2"); len(cs) != 0 {
		t.Fatalf("session s2 sees %+v", cs)
	}
	fresh := &Journal{dir: filepath.Join(dir, ".agentgo", "changes")} // as a restarted process would open it
	if cs := fresh.List("s1"); len(cs) != 1 || cs[0].CallID != "c1" || cs[0].Existed {
		t.Fatalf("after a restart: %+v", cs)
	}
	rep, err := fresh.Revert("s1", "")
	if err != nil || len(rep.Removed) != 1 {
		t.Fatalf("revert after restart: %+v %v", rep, err)
	}
}

// Parallel calls in one reply all land in the journal, none lost to a race.
func TestJournalKeepsEveryChangeOfParallelCalls(t *testing.T) {
	tools, dir, j := journalTools(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
			if _, err := fsTool(t, tools, "write").Run(context.Background(), "s", "same-reply", fsArgs(map[string]any{"file_path": p, "content": "x\n"})); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cs := j.List("s")
	if len(cs) != 12 {
		t.Fatalf("%d changes recorded, want 12", len(cs))
	}
	seen := map[int]bool{}
	for _, c := range cs {
		if seen[c.Seq] {
			t.Fatalf("duplicate seq %d", c.Seq)
		}
		seen[c.Seq] = true
	}
	rep, err := j.Revert("s", "")
	if err != nil || len(rep.Removed) != 12 {
		t.Fatalf("one undo must remove all 12 files made by one reply: %+v %v", rep, err)
	}
}

func TestJournalOffMeansNoUndoTools(t *testing.T) {
	for _, tl := range NewFSTools(FSOptions{WorkDir: t.TempDir()}) {
		if tl.Name == "revert_file_changes" || tl.Name == "file_changes" {
			t.Fatalf("%s offered without a journal", tl.Name)
		}
	}
}
