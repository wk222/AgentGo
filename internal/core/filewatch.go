package core

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

// File-change capture. The Eino engine reports tool calls, not file changes, so
// surfaces that want a diff (the coding_agent workflow node, the Crush TUI
// adapter) snapshot a file-changing tool's target when the call starts and diff
// it when the call finishes. ChangeWatcher is that capture, shared by both.

// maxSnapshotBytes bounds the files we copy for the sidebar diff.
const maxSnapshotBytes = 2 << 20

// shellTool can change any file; it is covered by a workspace scan instead.
const ShellTool = "execute_bash"

type fileSnap struct {
	path   string
	before string
	ok     bool // before was captured (missing file counts: it is being created)
}

// snapshotBefore captures the target of a file-changing tool call (editTools
// are the tool names whose calls change a file and carry it in "file_path").
func snapshotBefore(editTools map[string]bool, workDir, name, argsJSON string) (fileSnap, bool) {
	if !editTools[name] {
		return fileSnap{}, false
	}
	var a struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(argsJSON), &a) != nil || a.FilePath == "" {
		return fileSnap{}, false
	}
	p := a.FilePath
	if !filepath.IsAbs(p) {
		p = filepath.Join(workDir, p)
	}
	before, ok := readSnapshot(p)
	return fileSnap{path: p, before: before, ok: ok}, true
}

// readSnapshot returns the file content; a missing file is "" (creation).
func readSnapshot(p string) (string, bool) {
	fi, err := os.Stat(p)
	if os.IsNotExist(err) {
		return "", true
	}
	if err != nil || fi.IsDir() || fi.Size() > maxSnapshotBytes {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Bounds for the before/after workspace scan around shell commands. A shell
// command can edit any file, so unlike edit tools we cannot know the target.
const (
	treeMaxFiles   = 4000
	treeMaxFile    = 64 << 10 // per-file content cap
	treeMaxContent = 16 << 20 // total content cap
)

var treeSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".idea": true, ".vscode": true,
	"dist": true, "build": true, "target": true, "__pycache__": true, ".venv": true,
}

type treeEntry struct {
	size    int64
	modNano int64
	content string
	kept    bool // content captured (small text file)
}

type treeSnap map[string]treeEntry

// scanTree records size/mtime (and small text contents) of the workspace files.
func scanTree(root string) treeSnap {
	snap := treeSnap{}
	total := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && treeSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if len(snap) >= treeMaxFiles {
			return fs.SkipAll
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		e := treeEntry{size: fi.Size(), modNano: fi.ModTime().UnixNano()}
		if fi.Size() <= treeMaxFile && total+int(fi.Size()) <= treeMaxContent {
			if b, err := os.ReadFile(p); err == nil && !bytes.Contains(b, []byte{0}) {
				e.content, e.kept = string(b), true
				total += len(b)
			}
		}
		snap[p] = e
		return nil
	})
	return snap
}

// FileDelta is one file that differs between two scans.
type FileDelta struct{ Path, Before, After string }

// diffTrees lists text files created or modified between before and after.
// Files whose earlier content was not captured are skipped (no honest diff).
func diffTrees(before, after treeSnap) []FileDelta {
	var out []FileDelta
	for p, a := range after {
		if !a.kept {
			continue
		}
		b, existed := before[p]
		switch {
		case !existed:
			out = append(out, FileDelta{p, "", a.content})
		case b.kept && b.content != a.content:
			out = append(out, FileDelta{p, b.content, a.content})
		}
	}
	return out
}

// ChangeWatcher brackets tool calls: Start before a call runs, Finish after.
// It is not safe for concurrent use; callers hold their own lock.
type ChangeWatcher struct {
	WorkDir string
	// EditTools are the engine tool names whose calls change a file and carry
	// the target in "file_path". The kernel does not know tool names; the host
	// supplies them.
	EditTools map[string]bool

	pre   map[string]fileSnap // call id -> target before an edit tool ran
	trees map[string]treeSnap // call id -> workspace before a shell call
}

// Start snapshots what the call may change (edit tools: their target file;
// shell: the workspace).
func (w *ChangeWatcher) Start(id, name, args string) {
	if id == "" || w.WorkDir == "" {
		return
	}
	if snap, ok := snapshotBefore(w.EditTools, w.WorkDir, name, args); ok && snap.ok {
		if w.pre == nil {
			w.pre = map[string]fileSnap{}
		}
		w.pre[id] = snap
	}
	if name == ShellTool {
		if w.trees == nil {
			w.trees = map[string]treeSnap{}
		}
		w.trees[id] = scanTree(w.WorkDir)
	}
}

// Finish returns the files the call changed. A failed call changes nothing it
// reports for edit tools (a shell scan is reported regardless, as the command
// may have partly run). Edit-tool deltas can have Before == After.
func (w *ChangeWatcher) Finish(id string, isErr bool) []FileDelta {
	var out []FileDelta
	if snap, ok := w.pre[id]; ok {
		delete(w.pre, id)
		if !isErr {
			if after, ok := readSnapshot(snap.path); ok {
				out = append(out, FileDelta{snap.path, snap.before, after})
			}
		}
	}
	if before, ok := w.trees[id]; ok {
		delete(w.trees, id)
		out = append(out, diffTrees(before, scanTree(w.WorkDir))...)
	}
	return out
}
