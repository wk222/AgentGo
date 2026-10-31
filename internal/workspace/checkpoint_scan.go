package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type scanRequest struct {
	root    string
	ignores []string
	prev    *checkpoint // size+mtime cache source; nil reads every file
	store   bool        // keep the bytes of files that fit; false only hashes (previews)
	limits  CheckpointLimits
	known   func(blobKey) (size int64, ok bool) // blobs the store already holds
}

type scanResult struct {
	files   map[string]checkpointFile
	pending map[blobKey][]byte // blobs the commit adds to the store
	skipped int
}

// scanCheckpointFiles walks the tracked files of a workspace. It follows the same
// ignore rules as the review panel, so "what a rollback can touch" and "what the
// panel shows" are the same set. Unchanged files (same size and mtime as in prev)
// are not read again.
func scanCheckpointFiles(req scanRequest) (*scanResult, error) {
	ctx := context.Background()
	if req.limits.ScanTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.limits.ScanTimeout)
		defer cancel()
	}
	res := &scanResult{files: make(map[string]checkpointFile), pending: make(map[blobKey][]byte)}

	var cache map[string]checkpointFile
	var trustBefore time.Time
	if req.prev != nil && SamePath(req.prev.root, req.root) {
		cache = req.prev.files
		trustBefore = req.prev.createdAt.Add(-checkpointRacyWindow)
	}

	var pendingBytes int64
	visited := 0
	err := filepath.WalkDir(req.root, func(full string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			switch {
			case errors.Is(walkErr, fs.ErrPermission):
				res.skipped++ // a folder we may not read: leave it alone
				return nil
			case errors.Is(walkErr, fs.ErrNotExist):
				return nil // removed while we walked
			}
			return walkErr
		}
		if full == req.root {
			return nil
		}
		rel, err := filepath.Rel(req.root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if dirRel == "." {
			dirRel = ""
		}
		if shouldSkipReviewEntry(req.root, dirRel, entry.Name(), entry.IsDir(), req.ignores) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			res.skipped++ // links and devices are not tracked
			return nil
		}
		if visited++; visited > req.limits.MaxFiles {
			return errTooManyFiles
		}
		mod := info.ModTime()
		if ref, ok := cache[rel]; ok && !ref.Opaque && ref.Size == info.Size() &&
			ref.ModTime == mod.UnixNano() && !mod.After(trustBefore) {
			ref.Mode = info.Mode()
			res.files[rel] = ref
			return nil
		}
		res.files[rel] = res.capture(full, info, req, &pendingBytes)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if req.store {
		res.trimToBudget(req)
	}
	return res, nil
}

// capture reads one changed file. A file that cannot be read becomes opaque
// instead of vanishing from the manifest: a missing entry would later look like
// "created after the checkpoint" and a rollback would delete it.
func (r *scanResult) capture(full string, info os.FileInfo, req scanRequest, pendingBytes *int64) checkpointFile {
	f := checkpointFile{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Mode: info.Mode()}
	if !req.store || info.Size() > req.limits.MaxFileBytes {
		if err := hashFileStream(full, &f); err != nil {
			f = checkpointFile{Size: f.Size, ModTime: f.ModTime, Mode: f.Mode, Opaque: true}
		}
		if req.store {
			r.skipped++ // the bytes of this file are not kept
		}
		return f
	}
	data, err := os.ReadFile(full)
	if err != nil {
		r.skipped++
		return checkpointFile{Size: f.Size, ModTime: f.ModTime, Mode: f.Mode, Opaque: true}
	}
	f.Size = int64(len(data))
	f.Hash = sha256.Sum256(data)
	f.Binary = bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0
	if req.known != nil {
		if _, ok := req.known(f.Hash); ok {
			f.Restorable = true
			return f
		}
	}
	if _, dup := r.pending[f.Hash]; dup {
		f.Restorable = true
		return f
	}
	if *pendingBytes+int64(len(data)) > req.limits.MaxSessionBytes {
		r.skipped++ // over budget: keep the hash only
		return f
	}
	r.pending[f.Hash] = data
	*pendingBytes += int64(len(data))
	f.Restorable = true
	return f
}

// hashFileStream hashes a file without keeping it and notes whether it looks binary.
func hashFileStream(full string, f *checkpointFile) error {
	in, err := os.Open(full)
	if err != nil {
		return err
	}
	defer in.Close()
	sample := make([]byte, 8192)
	n, readErr := in.Read(sample)
	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	f.Binary = bytes.IndexByte(sample[:n], 0) >= 0
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	copy(f.Hash[:], h.Sum(nil))
	return nil
}

// trimToBudget enforces "one checkpoint references at most MaxSessionBytes". When
// the new manifest would reference more, the biggest blobs lose their bytes (the
// hash stays), new ones first, so memory never grows past the budget.
func (r *scanResult) trimToBudget(req scanRequest) {
	type item struct {
		hash    blobKey
		size    int64
		pending bool
	}
	seen := make(map[blobKey]bool)
	var items []item
	var total int64
	for _, f := range r.files {
		if !f.Restorable || seen[f.Hash] {
			continue
		}
		seen[f.Hash] = true
		if data, ok := r.pending[f.Hash]; ok {
			items = append(items, item{f.Hash, int64(len(data)), true})
			total += int64(len(data))
		} else if req.known != nil {
			if size, ok := req.known(f.Hash); ok {
				items = append(items, item{f.Hash, size, false})
				total += size
			}
		}
	}
	if total <= req.limits.MaxSessionBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].pending != items[j].pending {
			return items[i].pending
		}
		return items[i].size > items[j].size
	})
	demote := make(map[blobKey]bool)
	for _, it := range items {
		if total <= req.limits.MaxSessionBytes {
			break
		}
		demote[it.hash] = true
		total -= it.size
		delete(r.pending, it.hash)
	}
	for path, f := range r.files {
		if f.Restorable && demote[f.Hash] {
			f.Restorable = false
			r.files[path] = f
			r.skipped++
		}
	}
}
