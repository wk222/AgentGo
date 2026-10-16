package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/engine"
)

var testEditTools = map[string]bool{"code_edit": true, "code_multiedit": true, "code_write": true}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffTreesFindsModifiedAndCreatedTextFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "one\n")
	write(t, filepath.Join(root, "same.txt"), "same\n")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref\n")
	write(t, filepath.Join(root, "bin.dat"), "x\x00y")
	before := scanTree(root)

	write(t, filepath.Join(root, "a.txt"), "two\n")
	write(t, filepath.Join(root, "new.txt"), "fresh\n")
	write(t, filepath.Join(root, ".git", "HEAD"), "changed\n") // skipped dir
	write(t, filepath.Join(root, "bin.dat"), "z\x00w")         // binary: no honest diff
	got := map[string]FileDelta{}
	for _, d := range diffTrees(before, scanTree(root)) {
		got[filepath.Base(d.Path)] = d
	}
	if len(got) != 2 {
		t.Fatalf("deltas = %+v", got)
	}
	if d := got["a.txt"]; d.Before != "one\n" || d.After != "two\n" {
		t.Fatalf("a.txt delta = %+v", d)
	}
	if d := got["new.txt"]; d.Before != "" || d.After != "fresh\n" {
		t.Fatalf("new.txt delta = %+v", d)
	}
}

func TestDiffTreesSkipsFilesWithoutCapturedBefore(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big.txt")
	write(t, big, strings.Repeat("a", treeMaxFile+1)) // too large to snapshot
	before := scanTree(root)
	write(t, big, strings.Repeat("b", treeMaxFile+1))
	if d := diffTrees(before, scanTree(root)); len(d) != 0 {
		t.Fatalf("diff without a captured before-image: %+v", d)
	}
}

func TestSnapshotBeforeIgnoresOtherTools(t *testing.T) {
	if _, ok := snapshotBefore(testEditTools, t.TempDir(), "code_view", `{"file_path":"a.go"}`); ok {
		t.Fatal("read-only tool snapshotted")
	}
	if _, ok := snapshotBefore(testEditTools, t.TempDir(), "code_edit", `not json`); ok {
		t.Fatal("bad args snapshotted")
	}
	s, ok := snapshotBefore(testEditTools, t.TempDir(), "code_write", `{"file_path":"new.txt"}`)
	if !ok || !s.ok || s.before != "" {
		t.Fatalf("creation snapshot = %+v ok=%v", s, ok)
	}
}

// Eino reports tool calls, not file changes, so the sink derives them: it
// snapshots an edit tool's target at call time and diffs it at result time.
func TestCodingChangeSinkDerivesFileChangesFromToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &ChangeSink{WorkDir: dir, EditTools: testEditTools}
	call := func(typ engine.EventType, p map[string]any) { sink.Emit(engine.Event{Type: typ, Payload: p}) }
	edit := "code_edit"

	call(engine.EventToolCall, map[string]any{"id": "1", "name": edit, "arguments": `{"file_path":"a.txt"}`})
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(engine.EventToolResult, map[string]any{"id": "1", "name": edit, "output": "ok"})

	// A failed edit and a read-only call change nothing.
	call(engine.EventToolCall, map[string]any{"id": "2", "name": edit, "arguments": `{"file_path":"a.txt"}`})
	call(engine.EventToolResult, map[string]any{"id": "2", "name": edit, "output": "boom", "is_error": true})
	call(engine.EventToolCall, map[string]any{"id": "3", "name": "code_view", "arguments": `{"file_path":"a.txt"}`})
	call(engine.EventToolResult, map[string]any{"id": "3", "name": "code_view", "output": "v1"})

	// Creating a file: before is empty.
	newPath := filepath.Join(dir, "n.txt")
	call(engine.EventToolCall, map[string]any{"id": "4", "name": "code_write", "arguments": `{"file_path":"n.txt"}`})
	if err := os.WriteFile(newPath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(engine.EventToolResult, map[string]any{"id": "4", "name": "code_write", "output": "ok"})

	if sink.calls != 4 || len(sink.order) != 2 {
		t.Fatalf("calls=%d order=%v", sink.calls, sink.order)
	}
	if c := sink.changes[path]; c == nil || c.Before != "v0" || c.After != "v1" {
		t.Fatalf("a.txt = %+v", c)
	}
	if c := sink.changes[newPath]; c == nil || c.Before != "" || c.After != "hi" {
		t.Fatalf("n.txt = %+v", c)
	}
}
