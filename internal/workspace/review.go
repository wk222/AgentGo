package workspace

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxReviewFileBytes  int64 = 8 * 1024 * 1024
	maxReviewTotalBytes int64 = 128 * 1024 * 1024
)

type reviewFile struct {
	Content    []byte
	Hash       [sha256.Size]byte
	Mode       os.FileMode
	Size       int64
	ModTime    int64
	Restorable bool
	Binary     bool
}

type Review struct {
	Root      string
	CreatedAt time.Time
	Files     map[string]reviewFile
	Skipped   int
	Ignores   []string
}

type ReviewChange struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Reviewable bool   `json:"reviewable"`
	Binary     bool   `json:"binary"`
	BeforeSize int64  `json:"before_size"`
	AfterSize  int64  `json:"after_size"`
	Reason     string `json:"reason,omitempty"`
}

type ReviewStore struct {
	mu      sync.Mutex
	reviews map[string]*Review
}

func NewReviewStore() *ReviewStore {
	return &ReviewStore{reviews: make(map[string]*Review)}
}

func reviewSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "desktop"
	}
	return sessionID
}

func (s *ReviewStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviews = make(map[string]*Review)
}

func (s *ReviewStore) Begin(sessionID, root string) (resumed bool, pending int, skipped int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID = reviewSessionID(sessionID)
	if existing := s.reviews[sessionID]; existing != nil && SamePath(existing.Root, root) {
		changes, scanErr := collectReviewChanges(existing)
		if scanErr != nil {
			return false, 0, existing.Skipped, scanErr
		}
		if len(changes) > 0 {
			return true, len(changes), existing.Skipped, nil
		}
	}

	review, snapErr := snapshotWorkspace(root)
	if snapErr != nil {
		return false, 0, 0, snapErr
	}
	s.reviews[sessionID] = review
	return false, 0, review.Skipped, nil
}

func (s *ReviewStore) List(sessionID, currentRoot string) (*Review, []ReviewChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review := s.reviews[reviewSessionID(sessionID)]
	if review == nil {
		return nil, nil, nil
	}
	if !SamePath(review.Root, currentRoot) {
		return review, nil, fmt.Errorf("review belongs to another workspace: %s", review.Root)
	}
	changes, err := collectReviewChanges(review)
	return review, changes, err
}

func (s *ReviewStore) Preview(sessionID, currentRoot, relPath string) (ReviewChange, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review := s.reviews[reviewSessionID(sessionID)]
	if review == nil {
		return ReviewChange{}, "", "", fmt.Errorf("no active AI review")
	}
	if !SamePath(review.Root, currentRoot) {
		return ReviewChange{}, "", "", fmt.Errorf("review belongs to another workspace")
	}
	clean, _, err := FullPath(review.Root, relPath, false)
	if err != nil {
		return ReviewChange{}, "", "", err
	}
	changes, err := collectReviewChanges(review)
	if err != nil {
		return ReviewChange{}, "", "", err
	}
	for _, change := range changes {
		if change.Path != clean {
			continue
		}
		if change.Binary {
			return change, "", "", nil
		}
		var before, after []byte
		if original, ok := review.Files[clean]; ok {
			before = original.Content
		}
		if change.Status != "deleted" {
			_, full, _ := FullPath(review.Root, clean, false)
			after, err = os.ReadFile(full)
			if err != nil {
				return ReviewChange{}, "", "", err
			}
		}
		return change, string(before), string(after), nil
	}
	return ReviewChange{}, "", "", fmt.Errorf("change not found: %s", clean)
}

func (s *ReviewStore) Accept(sessionID, currentRoot, relPath string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !SamePath(review.Root, currentRoot) {
		return 0, fmt.Errorf("review belongs to another workspace")
	}
	clean, full, err := FullPath(review.Root, relPath, false)
	if err != nil {
		return 0, err
	}
	if info, statErr := os.Stat(full); statErr == nil && info.Mode().IsRegular() {
		review.Files[clean], err = captureReviewFile(full, info, maxReviewTotalBytes)
		if err != nil {
			return 0, err
		}
	} else if os.IsNotExist(statErr) {
		delete(review.Files, clean)
	} else if statErr != nil {
		return 0, statErr
	} else {
		return 0, fmt.Errorf("path is not a regular file")
	}
	changes, err := collectReviewChanges(review)
	if err == nil && len(changes) == 0 {
		delete(s.reviews, key)
	}
	return len(changes), err
}

func (s *ReviewStore) AcceptAll(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reviews, reviewSessionID(sessionID))
}

func (s *ReviewStore) Reject(sessionID, currentRoot, relPath string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !SamePath(review.Root, currentRoot) {
		return 0, fmt.Errorf("review belongs to another workspace")
	}
	clean, _, err := FullPath(review.Root, relPath, false)
	if err != nil {
		return 0, err
	}
	changes, err := collectReviewChanges(review)
	if err != nil {
		return 0, err
	}
	var target *ReviewChange
	for i := range changes {
		if changes[i].Path == clean {
			target = &changes[i]
			break
		}
	}
	if target == nil {
		return len(changes), fmt.Errorf("change not found: %s", clean)
	}
	if !target.Reviewable {
		return len(changes), fmt.Errorf("cannot safely restore %s: %s", clean, target.Reason)
	}
	if err := restoreReviewPath(review, clean); err != nil {
		return len(changes), err
	}
	remaining, err := collectReviewChanges(review)
	if err == nil && len(remaining) == 0 {
		delete(s.reviews, key)
	}
	return len(remaining), err
}

func (s *ReviewStore) RejectAll(sessionID, currentRoot string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !SamePath(review.Root, currentRoot) {
		return 0, fmt.Errorf("review belongs to another workspace")
	}
	changes, err := collectReviewChanges(review)
	if err != nil {
		return 0, err
	}
	for _, change := range changes {
		if !change.Reviewable {
			return len(changes), fmt.Errorf("cannot safely restore %s: %s", change.Path, change.Reason)
		}
	}
	for _, change := range changes {
		if err := restoreReviewPath(review, change.Path); err != nil {
			return len(changes), err
		}
	}
	delete(s.reviews, key)
	return 0, nil
}

func snapshotWorkspace(root string) (*Review, error) {
	review := &Review{Root: root, CreatedAt: time.Now(), Files: make(map[string]reviewFile), Ignores: LoadIgnore(root)}
	var stored int64
	err := filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == root {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := entry.Name()
		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if dirRel == "." {
			dirRel = ""
		}
		if shouldSkipReviewEntry(root, dirRel, name, entry.IsDir(), review.Ignores) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			review.Skipped++
			return nil
		}
		file, err := captureReviewFile(full, info, maxReviewTotalBytes-stored)
		if err != nil {
			return err
		}
		if file.Restorable {
			stored += int64(len(file.Content))
		} else {
			review.Skipped++
		}
		review.Files[rel] = file
		return nil
	})
	return review, err
}

func captureReviewFile(full string, info os.FileInfo, remaining int64) (reviewFile, error) {
	file := reviewFile{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	if info.Size() > maxReviewFileBytes || info.Size() > remaining {
		f, err := os.Open(full)
		if err != nil {
			return reviewFile{}, err
		}
		defer f.Close()
		sample := make([]byte, 8192)
		n, readErr := f.Read(sample)
		if readErr != nil && readErr != io.EOF {
			return reviewFile{}, readErr
		}
		file.Binary = bytes.IndexByte(sample[:n], 0) >= 0
		if _, err := f.Seek(0, 0); err != nil {
			return reviewFile{}, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return reviewFile{}, err
		}
		copy(file.Hash[:], h.Sum(nil))
		return file, nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return reviewFile{}, err
	}
	file.Content = b
	file.Hash = sha256.Sum256(b)
	file.Restorable = true
	file.Binary = bytes.IndexByte(b, 0) >= 0
	return file, nil
}

func collectReviewChanges(review *Review) ([]ReviewChange, error) {
	current, err := scanCurrentWorkspace(review)
	if err != nil {
		return nil, err
	}
	var changes []ReviewChange
	for path, before := range review.Files {
		after, exists := current.Files[path]
		if !exists {
			changes = append(changes, makeReviewChange(path, "deleted", before, reviewFile{}, before.Restorable))
			continue
		}
		if before.Hash != after.Hash {
			changes = append(changes, makeReviewChange(path, "modified", before, after, before.Restorable))
		}
	}
	for path, after := range current.Files {
		if _, exists := review.Files[path]; !exists {
			changes = append(changes, makeReviewChange(path, "added", reviewFile{}, after, true))
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// scanCurrentWorkspace avoids rereading unchanged file content on every UI refresh.
// Changed files are still hashed before being reported.
func scanCurrentWorkspace(review *Review) (*Review, error) {
	current := &Review{Root: review.Root, Files: make(map[string]reviewFile)}
	err := filepath.WalkDir(review.Root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == review.Root {
			return nil
		}
		rel, err := filepath.Rel(review.Root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if dirRel == "." {
			dirRel = ""
		}
		if shouldSkipReviewEntry(review.Root, dirRel, entry.Name(), entry.IsDir(), review.Ignores) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if before, ok := review.Files[rel]; ok && before.Size == info.Size() && before.ModTime == info.ModTime().UnixNano() {
			current.Files[rel] = reviewFile{Hash: before.Hash, Mode: info.Mode(), Size: info.Size(), ModTime: before.ModTime}
			return nil
		}
		file, err := captureReviewFile(full, info, 0)
		if err != nil {
			return err
		}
		current.Files[rel] = file
		return nil
	})
	return current, err
}

func makeReviewChange(path, status string, before, after reviewFile, reviewable bool) ReviewChange {
	binary := before.Binary || after.Binary
	change := ReviewChange{
		Path: path, Name: filepath.Base(path), Status: status, Reviewable: reviewable,
		Binary: binary, BeforeSize: before.Size, AfterSize: after.Size,
	}
	if !reviewable {
		change.Reason = "pre-run file exceeded the safe snapshot limit"
	}
	return change
}

func restoreReviewPath(review *Review, relPath string) error {
	_, full, err := FullPath(review.Root, relPath, false)
	if err != nil {
		return err
	}
	original, existed := review.Files[relPath]
	if !existed {
		info, statErr := os.Lstat(full)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to remove non-file path: %s", relPath)
		}
		return os.Remove(full)
	}
	if !original.Restorable {
		return fmt.Errorf("original content was not captured: %s", relPath)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	mode := original.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	return os.WriteFile(full, original.Content, mode)
}

func shouldSkipReviewEntry(root, dirRel, name string, isDir bool, patterns []string) bool {
	if isDir && (name == ".git" || defaultWorkspaceIgnores[name]) {
		return true
	}
	rel := filepath.ToSlash(filepath.Join(dirRel, name))
	for _, pattern := range patterns {
		if IgnorePatternMatches(pattern, rel, name, isDir) {
			return true
		}
	}
	return false
}
