package bridge

import (
	"os"
	"strings"
)

// EventFilesDropped carries the real paths of files dropped on the desktop
// window's chat area. A WebView only hands the page a File (name, size, time),
// so the desktop host asks WebView2 for the paths and sends them over this event.
const EventFilesDropped = "files:dropped"

// DroppedPath is one dropped file or folder.
type DroppedPath struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// droppedFilesPayload builds the event body: blank and repeated paths are dropped
// and folders are flagged, so the page can attach each one as the right kind.
func droppedFilesPayload(paths []string) map[string]any {
	out := make([]DroppedPath, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		key := strings.ToLower(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		isDir := false
		if fi, err := os.Stat(p); err == nil {
			isDir = fi.IsDir()
		}
		out = append(out, DroppedPath{Path: p, IsDir: isDir})
	}
	return map[string]any{"files": out}
}
