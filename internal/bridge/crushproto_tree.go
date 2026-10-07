package bridge

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
)

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

// fileDelta is one file that differs between two scans.
type fileDelta struct{ path, before, after string }

// diffTrees lists text files created or modified between before and after.
// Files whose earlier content was not captured are skipped (no honest diff).
func diffTrees(before, after treeSnap) []fileDelta {
	var out []fileDelta
	for p, a := range after {
		if !a.kept {
			continue
		}
		b, existed := before[p]
		switch {
		case !existed:
			out = append(out, fileDelta{p, "", a.content})
		case b.kept && b.content != a.content:
			out = append(out, fileDelta{p, b.content, a.content})
		}
	}
	return out
}
