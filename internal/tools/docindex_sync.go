package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agentgo/internal/docindex"
)

// index_documents action=sync keeps the index of ONE folder of the workspace in line with the disk:
// new files are added, changed files re-indexed, and files that are gone are dropped. It is built for
// folders too big for one call and for disks that are sometimes not there:
//
//   - a cursor remembers where the walk stopped, so the next call continues after it (a call handles at most
//     docIndexMaxFiles changed files; unchanged files are free and are not parsed again);
//   - a file counts as deleted only after one complete walk of the folder, with no unreadable corner, did not
//     see it AND the file is confirmed missing by the system ("does not exist", never "cannot read");
//   - an offline share, a permission error or a folder that suddenly holds nothing never removes anything.
//
// The state belongs to the workspace and the scan: another workspace has its own, and a late call from a
// replaced scan cannot move the cursor of the new one.

const docSyncMaxVisit = 20000 // files one call looks at

// A folder whose indexed files are ALL missing is held back from deletion: an emptied or unmounted folder looks
// exactly like that, and a real mass deletion is one `remove` away.
const docSyncAllMissingMin = 3

type syncOutcome struct {
	Folder     string `json:"folder"`
	ScanID     string `json:"scan_id"`
	Status     string `json:"status"` // running (call again), complete, incomplete
	Visited    int    `json:"visited"`
	Cursor     string `json:"cursor,omitempty"`
	Dropped    int    `json:"dropped"`
	Kept       int    `json:"kept_unconfirmed,omitempty"`
	Unreadable int    `json:"unreadable_entries,omitempty"`
	Held       string `json:"held_back,omitempty"`
	Summary    string `json:"summary"`
}

// walkLess orders slash-separated paths the way filepath.WalkDir visits them: by name within each directory.
// Plain string comparison differs ("a.txt" sorts before "a/b.txt" there, but the walk enters "a" first).
func walkLess(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

func (d *docIndexer) sync(ctx context.Context, root, rootAbs, scope string, in indexDocumentsInput, out *indexDocumentsOutput) error {
	if len(in.Paths) != 1 {
		return fmt.Errorf("sync takes exactly one folder in paths")
	}
	rel, abs, err := resolveWorkspacePath(root, in.Paths[0], true, true)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder", rel)
	}
	key := docindex.NormSyncRoot(rel)
	state, had, err := d.store.SyncGet(ctx, scope, key)
	if err != nil {
		return err
	}
	if !had || state.Status != docindex.SyncRunning || in.Restart {
		if state, err = d.store.SyncBegin(ctx, scope, key); err != nil {
			return err
		}
	}
	oc := &syncOutcome{Folder: key, ScanID: state.ScanID}
	out.Sync = oc

	cursor := state.Cursor
	var seen []string
	visited, work, unreadable := 0, 0, 0
	stopped := false
	flush := func() error {
		// Progress is saved even when the caller cancelled: the cursor is what makes the next call resume.
		err := d.store.SyncProgress(context.WithoutCancel(ctx), scope, key, state.ScanID, cursor, seen, len(seen), unreadable > 0)
		seen = nil
		return err
	}
	walkErr := filepath.WalkDir(abs, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			unreadable++
			if e != nil && e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			stopped = true
			return filepath.SkipAll
		}
		if e.Type()&fs.ModeSymlink != 0 {
			return nil // links are never followed out of the chosen tree
		}
		if e.IsDir() {
			name := e.Name()
			if path != abs && (strings.HasPrefix(name, ".") || docIndexSkipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !docIndexExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		r, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return nil
		}
		src := filepath.ToSlash(r)
		if cursor != "" && !walkLess(cursor, src) {
			return nil // an earlier call of this scan already handled it
		}
		if visited >= docSyncMaxVisit || work >= docIndexMaxFiles {
			stopped = true
			return filepath.SkipAll
		}
		res := d.indexOne(ctx, root, rootAbs, scope, src)
		if ctx.Err() != nil {
			stopped = true // cancelled while parsing: this file was not handled, the next call starts at it
			return filepath.SkipAll
		}
		out.record(res)
		if res.Status != "unchanged" {
			work++
		}
		// The walk saw the file, even if it could not be parsed: a parse failure is not a deletion.
		seen = append(seen, src)
		cursor = src
		visited++
		if len(seen) >= 200 {
			if err := flush(); err != nil {
				return err
			}
		}
		return nil
	})
	oc.Visited = visited
	oc.Unreadable = unreadable
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		unreadable++
		oc.Unreadable = unreadable
	}
	if err := flush(); err != nil {
		if errors.Is(err, docindex.ErrStaleScan) {
			return fmt.Errorf("another sync of %s replaced this one; run sync again", key)
		}
		return err
	}
	oc.Cursor = cursor
	if stopped {
		out.Truncated = true
		oc.Status = docindex.SyncRunning
		oc.Summary = fmt.Sprintf("walked up to %q, not finished: run sync on %s again to continue; nothing is treated as deleted until the whole folder has been walked", cursor, key)
		return nil
	}

	// The walk reached the end of the folder.
	cur, _, err := d.store.SyncGet(ctx, scope, key)
	if err != nil {
		return err
	}
	if cur.ScanID != state.ScanID {
		return fmt.Errorf("another sync of %s replaced this one; run sync again", key)
	}
	finish := func(status string) error {
		oc.Status = status
		if err := d.store.SyncFinish(ctx, scope, key, state.ScanID, status); err != nil {
			if errors.Is(err, docindex.ErrStaleScan) {
				return fmt.Errorf("another sync of %s replaced this one; run sync again", key)
			}
			return err
		}
		return nil
	}
	if cur.Incomplete || unreadable > 0 {
		oc.Summary = "the whole folder was walked but some entries could not be read: files that were not seen are kept in the index, nothing was treated as deleted"
		return finish(docindex.SyncIncomplete)
	}
	if _, err := os.ReadDir(abs); err != nil {
		oc.Summary = "the folder cannot be read now (offline or no permission?): nothing was treated as deleted"
		return finish(docindex.SyncIncomplete)
	}
	unseen, err := d.store.SyncUnseen(ctx, scope, key, state.ScanID)
	if err != nil {
		return err
	}
	indexed, err := d.store.List(ctx, scope, key)
	if err != nil {
		return err
	}
	if len(unseen) > 0 && len(unseen) == len(indexed) && len(indexed) >= docSyncAllMissingMin {
		oc.Held = fmt.Sprintf("all %d indexed files were not seen: this looks like an emptied or unmounted folder, so none were dropped; use index_documents action=remove if they are really gone", len(indexed))
		oc.Summary = oc.Held
		return finish(docindex.SyncComplete)
	}
	for _, src := range unseen {
		_, err := os.Stat(filepath.Join(rootAbs, filepath.FromSlash(src)))
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			oc.Kept++ // exists, or cannot be checked: the index keeps it
			continue
		}
		if _, err := d.store.Remove(ctx, scope, src); err != nil {
			out.record(indexFileResult{Path: src, Status: "failed", Reason: err.Error()})
			oc.Kept++
			continue
		}
		res := indexFileResult{Path: src, Status: "removed", Reason: "file no longer exists (confirmed after a complete walk)"}
		d.flag(ctx, &res, "deleted")
		out.record(res)
		oc.Dropped++
	}
	oc.Summary = fmt.Sprintf("the whole folder was walked: %d dropped because the file no longer exists", oc.Dropped)
	if oc.Kept > 0 {
		oc.Summary += fmt.Sprintf(", %d not seen but still present or unverifiable, kept", oc.Kept)
	}
	return finish(docindex.SyncComplete)
}
