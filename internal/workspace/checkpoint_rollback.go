package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// What a rollback does to one file.
const (
	ActionRestore  = "restore"  // the file exists but differs: write the checkpoint's bytes
	ActionRecreate = "recreate" // the file was deleted since: write it again
	ActionDelete   = "delete"   // the file was created since: remove it
)

// Why a rollback leaves a file alone. The rule behind all of them: a rollback
// never does something it could not undo afterwards.
const (
	reasonNotStored = "the original content was not kept (file over the size limit, or the memory budget was used up)"
	reasonNoBackup  = "the current content is too large to keep a safety copy, so replacing it could not be undone"
	reasonOpaque    = "the file could not be read when the checkpoint was taken"
)

// RollbackChange is one file a rollback would touch (or, when Skip is set, would leave alone).
type RollbackChange struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Action     string `json:"action"`
	BeforeSize int64  `json:"before_size"` // size now
	AfterSize  int64  `json:"after_size"`  // size after the rollback
	Skip       bool   `json:"skip"`
	Reason     string `json:"reason,omitempty"`
}

// RollbackPreview is what a rollback would do, computed without touching anything.
type RollbackPreview struct {
	Checkpoint CheckpointInfo   `json:"checkpoint"`
	Changes    []RollbackChange `json:"changes"` // sorted by path; skipped files included
	Restore    int              `json:"restore"`
	Recreate   int              `json:"recreate"`
	Delete     int              `json:"delete"`
	Skipped    int              `json:"skipped"`
	// Unverified counts files that could not be read (then or now); a rollback leaves them alone.
	Unverified int `json:"unverified"`
}

func (p *RollbackPreview) actionable() int { return p.Restore + p.Recreate + p.Delete }

// RollbackIssue is a file a rollback left alone (Skipped) or failed to write (Failed).
type RollbackIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type RollbackResult struct {
	Restored   int             `json:"restored"`
	Recreated  int             `json:"recreated"`
	Deleted    int             `json:"deleted"`
	Skipped    []RollbackIssue `json:"skipped"`
	Failed     []RollbackIssue `json:"failed"`
	Unverified int             `json:"unverified"`
	// UndoID is the safety checkpoint taken just before; rolling back to it undoes this rollback.
	UndoID string `json:"undo_id,omitempty"`
}

func (s *CheckpointStore) pin(sessionID, root, id string) (*checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.sessions[reviewSessionID(sessionID)]
	if sc == nil {
		return nil, ErrCheckpointNotFound
	}
	cp := sc.find(id)
	if cp == nil {
		return nil, ErrCheckpointNotFound
	}
	if !SamePath(cp.root, root) {
		return nil, ErrCheckpointRoot
	}
	cp.pins++
	return cp, nil
}

func (s *CheckpointStore) unpin(cp *checkpoint) {
	s.mu.Lock()
	cp.pins--
	s.mu.Unlock()
}

// Preview says what rolling back to a checkpoint would do. It reads the workspace and changes nothing.
func (s *CheckpointStore) Preview(sessionID, root, id string) (*RollbackPreview, error) {
	cp, err := s.pin(sessionID, root, id)
	if err != nil {
		return nil, err
	}
	defer s.unpin(cp)
	scan, err := scanCheckpointFiles(scanRequest{root: root, ignores: cp.ignores, prev: cp, limits: s.limits})
	if err != nil {
		return nil, err
	}
	return buildPreview(cp, scan.files, s.limits), nil
}

func buildPreview(cp *checkpoint, current map[string]checkpointFile, limits CheckpointLimits) *RollbackPreview {
	// A preview does not store anything; whether the safety copy would fit is judged by size.
	fits := func(f checkpointFile) bool { return f.Size <= limits.MaxFileBytes }
	changes, unverified := diffCheckpoint(cp.files, current, fits)
	p := &RollbackPreview{Checkpoint: cp.info(), Changes: changes, Unverified: unverified}
	for _, c := range changes {
		switch {
		case c.Skip:
			p.Skipped++
		case c.Action == ActionRestore:
			p.Restore++
		case c.Action == ActionRecreate:
			p.Recreate++
		case c.Action == ActionDelete:
			p.Delete++
		}
	}
	return p
}

// diffCheckpoint compares the checkpoint's manifest with the files as they are
// now. canKeep says whether the safety checkpoint holds (or would hold) a file's
// current bytes, which decides if overwriting or deleting it can be undone.
func diffCheckpoint(target, current map[string]checkpointFile, canKeep func(checkpointFile) bool) (changes []RollbackChange, unverified int) {
	for p, want := range target {
		now, ok := current[p]
		if !ok {
			c := RollbackChange{Path: p, Name: path.Base(p), Action: ActionRecreate, AfterSize: want.Size}
			switch {
			case want.Opaque:
				c.Skip, c.Reason = true, reasonOpaque
			case !want.Restorable:
				c.Skip, c.Reason = true, reasonNotStored
			}
			changes = append(changes, c)
			continue
		}
		if want.Opaque || now.Opaque {
			unverified++
			continue
		}
		if want.Hash == now.Hash {
			continue
		}
		c := RollbackChange{Path: p, Name: path.Base(p), Action: ActionRestore, BeforeSize: now.Size, AfterSize: want.Size}
		switch {
		case !want.Restorable:
			c.Skip, c.Reason = true, reasonNotStored
		case !canKeep(now):
			c.Skip, c.Reason = true, reasonNoBackup
		}
		changes = append(changes, c)
	}
	for p, now := range current {
		if _, ok := target[p]; ok {
			continue
		}
		if now.Opaque {
			unverified++
			continue
		}
		c := RollbackChange{Path: p, Name: path.Base(p), Action: ActionDelete, BeforeSize: now.Size}
		if !canKeep(now) {
			c.Skip, c.Reason = true, reasonNoBackup
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, unverified
}

func skippedOf(changes []RollbackChange) []RollbackIssue {
	out := []RollbackIssue{}
	for _, c := range changes {
		if c.Skip {
			out = append(out, RollbackIssue{Path: c.Path, Reason: c.Reason})
		}
	}
	return out
}

// Rollback puts the tracked files back to the state of a checkpoint.
//
// Order: a read-only check, then a safety checkpoint of the current state, then
// the writes. It only touches files it can put back again: a file whose original
// bytes were not kept, or whose current bytes cannot be kept, is left alone and
// reported as skipped. Files created after the checkpoint are removed; folders and
// files in ignored places are never touched. A write that fails does not stop the
// rest; it is listed in the result, and the safety checkpoint (UndoID) can undo
// whatever did happen.
func (s *CheckpointStore) Rollback(sessionID, root, id string) (*RollbackResult, error) {
	target, err := s.pin(sessionID, root, id)
	if err != nil {
		return nil, err
	}
	defer s.unpin(target)

	scan, err := scanCheckpointFiles(scanRequest{root: root, ignores: target.ignores, prev: target, limits: s.limits})
	if err != nil {
		return nil, err
	}
	preview := buildPreview(target, scan.files, s.limits)
	if preview.actionable() == 0 {
		return &RollbackResult{Skipped: skippedOf(preview.Changes), Failed: []RollbackIssue{}, Unverified: preview.Unverified}, nil
	}

	// The safety checkpoint is also the authoritative description of "now".
	pre, err := s.begin(target.sessionID, root, "", CheckpointPreRollback, target.ignores)
	if err != nil {
		return nil, fmt.Errorf("could not save the current state before rolling back: %w", err)
	}
	changes, unverified := diffCheckpoint(target.files, pre.files, func(f checkpointFile) bool { return f.Restorable })

	res := &RollbackResult{UndoID: pre.id, Unverified: unverified, Skipped: skippedOf(changes), Failed: []RollbackIssue{}}
	rootReal := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		rootReal = r
	}
	apply := func(c RollbackChange) error {
		full, err := safeTarget(rootReal, root, c.Path)
		if err != nil {
			return err
		}
		if c.Action == ActionDelete {
			return removeFile(full)
		}
		want := target.files[c.Path]
		data, ok := s.blobData(target.sessionID, want.Hash)
		if !ok {
			return errors.New("the stored content is no longer available")
		}
		return writeFileAtomic(full, data, want.Mode)
	}
	// Write first, delete last: a failure half-way never leaves fewer files than before.
	for _, deletes := range []bool{false, true} {
		for _, c := range changes {
			if c.Skip || (c.Action == ActionDelete) != deletes {
				continue
			}
			if err := apply(c); err != nil {
				res.Failed = append(res.Failed, RollbackIssue{Path: c.Path, Reason: err.Error()})
				continue
			}
			switch c.Action {
			case ActionRestore:
				res.Restored++
			case ActionRecreate:
				res.Recreated++
			case ActionDelete:
				res.Deleted++
			}
		}
	}
	return res, nil
}

func (s *CheckpointStore) blobData(sessionID string, h blobKey) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc := s.sessions[sessionID]; sc != nil {
		if b := sc.blobs[h]; b != nil {
			return b.data, true
		}
	}
	return nil, false
}

// safeTarget resolves a manifest path to a file that is safe to write or remove:
// inside the workspace even after links are followed, and not a folder or link.
func safeTarget(rootReal, root, rel string) (string, error) {
	_, full, err := FullPath(root, rel, false)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(full)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if !insideDir(rootReal, realDir) {
		return "", fmt.Errorf("refusing to touch %s: its folder leads outside the workspace", rel)
	}
	if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing to touch %s: something other than a file is there now", rel)
	}
	return full, nil
}

func insideDir(root, p string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root, p = strings.ToLower(root), strings.ToLower(p)
	}
	root, p = filepath.Clean(root), filepath.Clean(p)
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// writeFileAtomic replaces a file without ever leaving it half written.
func writeFileAtomic(full string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agentgo-restore-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(name)
		return err
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	_ = os.Chmod(name, perm)
	if err := os.Rename(name, full); err != nil {
		// Windows refuses to replace a read-only file.
		_ = os.Chmod(full, 0o666)
		if err2 := os.Rename(name, full); err2 != nil {
			_ = os.Remove(name)
			return err
		}
	}
	return nil
}

func removeFile(full string) error {
	err := os.Remove(full)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Chmod(full, 0o666) // read-only files refuse to be removed on Windows
		err = os.Remove(full)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
