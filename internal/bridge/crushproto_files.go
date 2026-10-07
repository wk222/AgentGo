package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"

	"agentgo/internal/codetools"
)

// maxSnapshotBytes bounds the files we copy for the sidebar diff.
const maxSnapshotBytes = 2 << 20

// editTools are the engine tool names whose calls change a file and carry the
// target in "file_path".
var editTools = map[string]bool{
	codetools.ToolName("edit"):      true,
	codetools.ToolName("multiedit"): true,
	codetools.ToolName("write"):     true,
}

// shellTool can change any file; it is covered by a workspace scan instead.
const shellTool = "execute_bash"

type fileSnap struct {
	path   string
	before string
	ok     bool // before was captured (missing file counts: it is being created)
}

// snapshotBefore captures the target of a file-changing tool call.
func snapshotBefore(workDir, name, argsJSON string) (fileSnap, bool) {
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
