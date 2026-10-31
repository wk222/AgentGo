// Package artifact turns "the agent says it made a file" into a checked, versioned delivery.
//
// A delivery is real only when the file exists and is a regular file. Its bytes are copied into the folder the
// app already serves for downloads (so the user gets the version that was delivered even if the original is later
// changed or deleted), and the delivery is written to the conversation as a record, so it survives a restart,
// follows the conversation when it is forked or deleted, and never needs a second store.
//
// Versions are per conversation and path: delivering the same bytes again is "unchanged" and adds nothing;
// different bytes make the next version. A file the user removed from the deliverables is not attached again
// while its bytes stay the same (no coming back from the dead), but changed bytes are a new version.
//
// Standard library only.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxBytes is the largest file that can be delivered.
const MaxBytes = 200 << 20

// Delivery states.
const (
	StatusNew        = "new"
	StatusNewVersion = "new_version"
	StatusUnchanged  = "unchanged"
	StatusRemoved    = "removed"
)

// Record is one delivery as stored in the conversation.
type Record struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Version  int    `json:"version"`
	Status   string `json:"status"`
	Snapshot string `json:"snapshot"` // file name inside the download folder
	URL      string `json:"url"`
	At       string `json:"at"`
	// Attached is false when nothing new was added to the conversation (unchanged, or removed by the user).
	Attached bool   `json:"attached"`
	Note     string `json:"note,omitempty"`
}

// Store keeps the records of one conversation.
type Store interface {
	// Latest returns the newest record for path in the conversation.
	Latest(ctx context.Context, sessionID, path string) (Record, bool, error)
	Save(ctx context.Context, sessionID string, r Record) error
}

type env struct {
	sessionID string
	store     Store
	dir       string
}

type ctxKey struct{}

// WithStore makes deliveries in ctx belong to sessionID. dir is the download folder (served at /attachments/).
func WithStore(ctx context.Context, sessionID string, s Store, dir string) context.Context {
	if s == nil || strings.TrimSpace(sessionID) == "" || dir == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, &env{sessionID: sessionID, store: s, dir: dir})
}

// ErrNoSession means the call is not inside a conversation that can keep records.
var ErrNoSession = errors.New("deliveries need a conversation with a record to keep them in; none is available here")

// Deliver checks, snapshots and records path.
func Deliver(ctx context.Context, path, title string) (Record, error) {
	e, _ := ctx.Value(ctxKey{}).(*env)
	if e == nil {
		return Record{}, ErrNoSession
	}
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil {
		return Record{}, fmt.Errorf("%s does not exist, so nothing was delivered", path)
	}
	if !st.Mode().IsRegular() {
		return Record{}, fmt.Errorf("%s is not a regular file; deliver a file (zip a folder first)", path)
	}
	if st.Size() > MaxBytes {
		return Record{}, fmt.Errorf("%s is %d MB; the limit is %d MB", path, st.Size()>>20, MaxBytes>>20)
	}
	sum, size, err := hashFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading %s: %w", path, err)
	}
	prev, had, err := e.store.Latest(ctx, e.sessionID, path)
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if had && prev.SHA256 == sum {
		prev.Attached = false
		if prev.Status == StatusRemoved {
			prev.Note = "The user removed this file from the deliverables and its content has not changed, so it was not attached again."
			return prev, nil
		}
		prev.Status = StatusUnchanged
		prev.Note = "Same content as the version already delivered; nothing new was attached."
		return prev, nil
	}
	rec := Record{
		Path: path, Name: filepath.Base(path), Title: strings.TrimSpace(title), Size: size, SHA256: sum,
		Version: 1, Status: StatusNew, At: now, Attached: true,
	}
	if had {
		rec.Version, rec.Status = prev.Version+1, StatusNewVersion
	}
	rec.ID = fmt.Sprintf("ar_%s_v%d", shortHash(e.sessionID+"|"+path), rec.Version)
	rec.Snapshot = snapshotName(rec)
	if err := copySnapshot(path, filepath.Join(e.dir, rec.Snapshot), sum); err != nil {
		return Record{}, err
	}
	rec.URL = "/attachments/" + rec.Snapshot
	if err := e.store.Save(ctx, e.sessionID, rec); err != nil {
		_ = os.Remove(filepath.Join(e.dir, rec.Snapshot))
		return Record{}, fmt.Errorf("could not record the delivery: %w", err)
	}
	return rec, nil
}

// Tombstone marks a delivery as removed by the user and deletes its snapshot. It returns the stored record.
func Tombstone(ctx context.Context, store Store, dir, sessionID string, r Record) (Record, error) {
	r.Status, r.Attached, r.Note = StatusRemoved, false, ""
	r.At = time.Now().UTC().Format(time.RFC3339)
	if err := store.Save(ctx, sessionID, r); err != nil {
		return Record{}, err
	}
	if r.Snapshot != "" && dir != "" && !strings.ContainsAny(r.Snapshot, `/\`) {
		_ = os.Remove(filepath.Join(dir, r.Snapshot))
	}
	return r, nil
}

func snapshotName(r Record) string {
	return fmt.Sprintf("artifact-%s-v%d-%s", r.SHA256[:8], r.Version, safeFileName(r.Name))
}

func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r > 127:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "file"
	}
	return out
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copySnapshot copies src to dst through a temporary name and checks the copy has the hash that was recorded:
// a file that changes while it is being copied is not delivered half-and-half.
func copySnapshot(src, dst, wantHash string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != wantHash {
		err = errors.New("the file changed while it was being delivered; try again when it is finished being written")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
