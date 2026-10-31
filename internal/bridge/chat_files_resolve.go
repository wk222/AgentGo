package bridge

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A browser tells the page a dropped file's name, size and modification time,
// never where it lives. The chat only needs the path, and the server runs on the
// same PC, so it looks for the one file on disk that has all three. That is
// reliable enough for what people actually drop (something from the desktop,
// downloads, a chat tool's receive folder or the workspace), and when nothing
// matches the caller falls back to importing a copy.

const (
	resolveBudget     = 4 * time.Second
	resolveMaxEntries = 300000
	mtimeSlackMS      = 2000 // FAT stores 2 s steps; JS rounds to ms
)

type searchRoot struct {
	dir   string
	depth int // how many folder levels below dir to look
}

// skippedDirs are never worth walking: they hold thousands of files nobody drops.
var skippedDirs = map[string]bool{
	"node_modules": true, ".git": true, "$recycle.bin": true, "system volume information": true,
	"appdata": true, "windows": true, "program files": true, "program files (x86)": true,
	".cache": true, "__pycache__": true, ".venv": true, "venv": true,
}

// ResolveDroppedFile finds the real path of a file dropped into the chat.
// It returns {success:true, path} or {success:false}; it never reads contents.
func (s *AppService) ResolveDroppedFile(name string, size, modifiedMS int64) map[string]any {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "" || name == "." || size < 0 {
		return map[string]any{"success": false}
	}
	ctx, cancel := context.WithTimeout(context.Background(), resolveBudget)
	defer cancel()
	if p := findDroppedFile(ctx, defaultSearchRoots(s.rt.WorkspaceRoot()), name, size, modifiedMS); p != "" {
		return map[string]any{"success": true, "path": p}
	}
	return map[string]any{"success": false}
}

// defaultSearchRoots lists where dropped files usually come from, closest first.
func defaultSearchRoots(workspace string) []searchRoot {
	var roots []searchRoot
	add := func(dir string, depth int) {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, searchRoot{dir, depth})
		}
	}
	home, _ := os.UserHomeDir()
	oneDrive := os.Getenv("OneDrive")
	for _, base := range []string{home, oneDrive} {
		if base == "" {
			continue
		}
		add(filepath.Join(base, "Desktop"), 4)
		add(filepath.Join(base, "Downloads"), 4)
	}
	add(workspace, 6)
	for _, base := range []string{home, oneDrive} {
		if base != "" {
			// WeChat, DingTalk and WeCom keep received files several levels down here.
			add(filepath.Join(base, "Documents"), 8)
		}
	}
	if runtime.GOOS == "windows" {
		add(os.Getenv("PUBLIC"), 0)
	}
	return roots
}

// findDroppedFile walks the roots in order and returns the first file named name
// (case-insensitively) with this size and a modification time within the slack.
// modifiedMS <= 0 means the browser did not report one: name and size decide.
func findDroppedFile(ctx context.Context, roots []searchRoot, name string, size, modifiedMS int64) string {
	want := strings.ToLower(name)
	seen := map[string]bool{}
	entries := 0
	for _, root := range roots {
		dir := filepath.Clean(root.dir)
		if seen[strings.ToLower(dir)] {
			continue
		}
		seen[strings.ToLower(dir)] = true
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		var found string
		baseDepth := strings.Count(dir, string(filepath.Separator))
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entries++
			if entries > resolveMaxEntries {
				return fs.SkipAll
			}
			if d.IsDir() {
				if path != dir && (skippedDirs[strings.ToLower(d.Name())] || strings.Count(path, string(filepath.Separator))-baseDepth > root.depth) {
					return fs.SkipDir
				}
				return nil
			}
			if strings.ToLower(d.Name()) != want {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() != size {
				return nil
			}
			if modifiedMS > 0 {
				diff := info.ModTime().UnixMilli() - modifiedMS
				if diff < -mtimeSlackMS || diff > mtimeSlackMS {
					return nil
				}
			}
			found = path
			return fs.SkipAll
		})
		if found != "" {
			return found
		}
		if ctx.Err() != nil {
			return ""
		}
	}
	return ""
}
