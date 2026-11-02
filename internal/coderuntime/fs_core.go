package coderuntime

// The file tools (view, write, edit, multiedit) share one small engine: a
// per-session record of what was viewed, a freshness check against it, and a
// single persist step that every mutation goes through (secret guard,
// approval, backup, write, journal). Approval is one hook (FSOptions.Approve);
// the governance layer above is the real gate.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultReadLimit is how many lines view returns when limit is not given.
	DefaultReadLimit = 200
	// MaxViewSize caps the bytes one view call may return.
	MaxViewSize = 200 * 1024
	// MaxLineLength truncates very long lines in view output.
	MaxLineLength = 2000
)

// FSRequest is what Approve is asked about.
type FSRequest struct {
	SessionID   string
	CallID      string
	Tool        string // view | write | edit | multiedit
	Action      string // read | write
	Path        string // absolute
	Description string
}

// FSOptions configures NewFSTools.
type FSOptions struct {
	WorkDir string
	// Approve gates reads outside WorkDir and every write. nil allows all.
	Approve func(ctx context.Context, req FSRequest) bool
	// AfterChange runs after a successful write/edit and returns text (for
	// example language-server diagnostics) appended to the tool result.
	AfterChange func(ctx context.Context, sessionID, callID, path string) string
	// OnRead runs after a successful view, e.g. to warm up a language server.
	OnRead func(ctx context.Context, path string)
	// BackupDir receives a copy of a file's previous bytes before an overwrite.
	// Empty means <WorkDir>/.agentgo/overwrite-backups.
	BackupDir string
	// DisableBackup turns the pre-overwrite backup off.
	DisableBackup bool
	// Journal, when set, records every successful write/edit/multiedit so a
	// session's file changes can be listed and undone (see journal.go).
	Journal *Journal
	// DisableSecretGuard lets the agent write credential-shaped content.
	// AGENTGO_ALLOW_SECRET_WRITES=1 does the same without code changes.
	DisableSecretGuard bool
	// ReadLimit is how many lines view returns when the model gives no limit.
	// Zero means DefaultReadLimit.
	ReadLimit int
}

// readLimit is the number of lines view shows by default.
func (o FSOptions) readLimit() int {
	if o.ReadLimit > 0 {
		return o.ReadLimit
	}
	return DefaultReadLimit
}

type fsTools struct {
	opts FSOptions
	root string

	viewedMu sync.Mutex
	viewed   map[string]time.Time // session + path -> when it was last viewed

	// locks serialises the check-approve-write sequence on one file (fs_lock.go).
	locks pathLocks
}

// NewFSTools returns the view, write, edit and multiedit tools (plus the
// change-journal tools when a journal is configured).
func NewFSTools(opts FSOptions) []Tool {
	t := &fsTools{opts: opts, viewed: map[string]time.Time{}}
	t.root, _ = filepath.Abs(opts.WorkDir)
	switch {
	case opts.DisableBackup:
		t.opts.BackupDir = ""
	case opts.BackupDir == "":
		t.opts.BackupDir = filepath.Join(t.root, ".agentgo", "overwrite-backups")
	}
	out := []Tool{t.viewTool(), t.writeTool(), t.editTool(), t.multiEditTool()}
	if opts.Journal != nil {
		out = append(out, t.changesTool(), t.revertTool())
	}
	return out
}

func (t *fsTools) approve(ctx context.Context, r FSRequest) bool {
	return t.opts.Approve == nil || t.opts.Approve(ctx, r)
}

// resolve anchors a model-supplied path at the workspace. A leading slash counts
// as absolute on every OS: models write POSIX-style paths on Windows too.
func (t *fsTools) resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(filepath.ToSlash(p), "/") {
		return p
	}
	return filepath.Join(t.opts.WorkDir, p)
}

func (t *fsTools) outsideWorkDir(abs string) bool {
	a, err := filepath.Abs(abs)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(t.root, a)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// --- what the session has viewed ---------------------------------------------

func viewKey(sessionID, path string) string { return sessionID + "\x00" + filepath.Clean(path) }

func (t *fsTools) markViewed(sessionID, path string) {
	t.viewedMu.Lock()
	t.viewed[viewKey(sessionID, path)] = time.Now()
	t.viewedMu.Unlock()
}

func (t *fsTools) viewedAt(sessionID, path string) time.Time {
	t.viewedMu.Lock()
	defer t.viewedMu.Unlock()
	return t.viewed[viewKey(sessionID, path)]
}

// verifyFresh refuses a change to a file the session never viewed or that
// moved on disk since. The comparison is in whole seconds: a view and a write
// within the same second do not count as "modified after".
func (t *fsTools) verifyFresh(sessionID, path string, info os.FileInfo) *ToolResult {
	seen := t.viewedAt(sessionID, path)
	if seen.IsZero() {
		r := neverViewed(path)
		return &r
	}
	seen = seen.Truncate(time.Second)
	if mod := info.ModTime().Truncate(time.Second); mod.After(seen) {
		r := changedAfterView(path, mod, seen)
		return &r
	}
	return nil
}

// --- line endings ------------------------------------------------------------

// splitEOL returns text with LF endings and whether it used CRLF.
func splitEOL(s string) (text string, crlf bool) {
	if strings.Contains(s, "\r\n") {
		return strings.ReplaceAll(s, "\r\n", "\n"), true
	}
	return s, false
}

// joinEOL gives LF text uniform CRLF endings again.
func joinEOL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// --- the one mutation path ---------------------------------------------------

// change describes one file mutation, after the checks on the old contents.
type change struct {
	tool    string // view-facing tool name for approval and the journal
	path    string
	desc    string // shown to the approver
	before  []byte // the previous bytes, when they could be read
	existed bool
	readOK  bool // before holds the file's real previous bytes
	after   []byte
}

// persist is where every mutation lands: secret guard, approval, parent
// directories, backup, write, journal and the "viewed" mark. refusal is
// non-nil when the change was not made for a reason the model should see.
func (t *fsTools) persist(ctx context.Context, sessionID, callID string, c change) (note string, refusal *ToolResult, err error) {
	if r := t.guardContent(c.path, string(c.before), string(c.after)); r != nil {
		return "", r, nil
	}
	req := FSRequest{SessionID: sessionID, CallID: callID, Tool: c.tool, Action: "write", Path: c.path, Description: c.desc}
	if !t.approve(ctx, req) {
		r := errResult(refusedByUser)
		return "", &r, nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return "", nil, fmt.Errorf("cannot create the parent folders of %s: %w", c.path, err)
	}
	saved := ""
	if c.existed {
		saved = t.backupBeforeOverwrite(c.path)
	}
	if err := os.WriteFile(c.path, c.after, 0o644); err != nil {
		return "", nil, fmt.Errorf("cannot write %s: %w", c.path, err)
	}
	t.record(sessionID, callID, c)
	t.markViewed(sessionID, c.path)
	return backupNote(saved), nil, nil
}

// record journals a change. A file whose previous bytes were never read is
// journalled as not restorable.
func (t *fsTools) record(sessionID, callID string, c change) {
	j := t.opts.Journal
	if j == nil {
		return
	}
	if c.existed && !c.readOK {
		j.recordChange(sessionID, callID, c.tool, c.path, nil, true, false, c.after)
		return
	}
	j.Record(sessionID, callID, c.tool, c.path, c.before, c.existed, c.after)
}

func (t *fsTools) afterChange(ctx context.Context, sessionID, callID, path string) string {
	if t.opts.AfterChange == nil {
		return ""
	}
	return t.opts.AfterChange(ctx, sessionID, callID, path)
}
