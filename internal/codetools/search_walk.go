package codetools

import (
	"context"
	"os"
	"path/filepath"
	"sort"
)

type walkAction int

const (
	walkContinue walkAction = iota
	walkSkipDir
	walkStop
)

type walkOpts struct {
	FilesFirst bool // list a directory's files before its subdirectories
	Follow     bool // descend into symlinked directories (loops are cut)
	MaxDepth   int  // levels below root to visit; <= 0 means unlimited
}

// walkTree visits root, then everything below it depth-first in a stable
// order (lexical, optionally files before subdirectories). Unreadable entries are
// skipped. It stops when ctx ends or fn returns walkStop.
func walkTree(ctx context.Context, root string, o walkOpts, fn func(path string, isDir bool) walkAction) {
	visited := map[string]bool{}
	var rec func(dir string, level int) bool // false = stop everything
	rec = func(dir string, level int) bool {
		if o.MaxDepth > 0 && level >= o.MaxDepth {
			return true
		}
		if o.Follow {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil || visited[real] {
				return true
			}
			visited[real] = true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		type ent struct {
			path string
			dir  bool
		}
		list := make([]ent, 0, len(entries))
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			isDir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 && o.Follow {
				if st, err := os.Stat(p); err == nil {
					isDir = st.IsDir()
				}
			}
			list = append(list, ent{p, isDir})
		}
		if o.FilesFirst {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].dir && list[j].dir })
		}
		for _, e := range list {
			if ctx.Err() != nil {
				return false
			}
			switch fn(e.path, e.dir) {
			case walkStop:
				return false
			case walkSkipDir:
				continue
			}
			if e.dir && !rec(e.path, level+1) {
				return false
			}
		}
		return true
	}
	if fn(root, true) == walkContinue {
		rec(root, 0)
	}
}
