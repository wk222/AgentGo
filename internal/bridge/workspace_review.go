package bridge

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

type workspaceReview struct {
	Root      string
	CreatedAt time.Time
	Files     map[string]reviewFile
	Skipped   int
	Ignores   []string
}

type WorkspaceReviewChange struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Reviewable bool   `json:"reviewable"`
	Binary     bool   `json:"binary"`
	BeforeSize int64  `json:"before_size"`
	AfterSize  int64  `json:"after_size"`
	Reason     string `json:"reason,omitempty"`
}

type workspaceReviewStore struct {
	mu      sync.Mutex
	reviews map[string]*workspaceReview
}

func newWorkspaceReviewStore() *workspaceReviewStore {
	return &workspaceReviewStore{reviews: make(map[string]*workspaceReview)}
}

func reviewSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "desktop"
	}
	return sessionID
}

func (s *workspaceReviewStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviews = make(map[string]*workspaceReview)
}

func (s *workspaceReviewStore) begin(sessionID, root string) (resumed bool, pending int, skipped int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID = reviewSessionID(sessionID)
	if existing := s.reviews[sessionID]; existing != nil && samePath(existing.Root, root) {
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

func (s *workspaceReviewStore) list(sessionID, currentRoot string) (*workspaceReview, []WorkspaceReviewChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review := s.reviews[reviewSessionID(sessionID)]
	if review == nil {
		return nil, nil, nil
	}
	if !samePath(review.Root, currentRoot) {
		return review, nil, fmt.Errorf("review belongs to another workspace: %s", review.Root)
	}
	changes, err := collectReviewChanges(review)
	return review, changes, err
}

func (s *workspaceReviewStore) preview(sessionID, currentRoot, relPath string) (WorkspaceReviewChange, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review := s.reviews[reviewSessionID(sessionID)]
	if review == nil {
		return WorkspaceReviewChange{}, "", "", fmt.Errorf("no active AI review")
	}
	if !samePath(review.Root, currentRoot) {
		return WorkspaceReviewChange{}, "", "", fmt.Errorf("review belongs to another workspace")
	}
	clean, _, err := workspaceFullPath(review.Root, relPath, false)
	if err != nil {
		return WorkspaceReviewChange{}, "", "", err
	}
	changes, err := collectReviewChanges(review)
	if err != nil {
		return WorkspaceReviewChange{}, "", "", err
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
			_, full, _ := workspaceFullPath(review.Root, clean, false)
			after, err = os.ReadFile(full)
			if err != nil {
				return WorkspaceReviewChange{}, "", "", err
			}
		}
		return change, string(before), string(after), nil
	}
	return WorkspaceReviewChange{}, "", "", fmt.Errorf("change not found: %s", clean)
}

func (s *workspaceReviewStore) accept(sessionID, currentRoot, relPath string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !samePath(review.Root, currentRoot) {
		return 0, fmt.Errorf("review belongs to another workspace")
	}
	clean, full, err := workspaceFullPath(review.Root, relPath, false)
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

func (s *workspaceReviewStore) acceptAll(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reviews, reviewSessionID(sessionID))
}

func (s *workspaceReviewStore) reject(sessionID, currentRoot, relPath string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !samePath(review.Root, currentRoot) {
		return 0, fmt.Errorf("review belongs to another workspace")
	}
	clean, _, err := workspaceFullPath(review.Root, relPath, false)
	if err != nil {
		return 0, err
	}
	changes, err := collectReviewChanges(review)
	if err != nil {
		return 0, err
	}
	var target *WorkspaceReviewChange
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

func (s *workspaceReviewStore) rejectAll(sessionID, currentRoot string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewSessionID(sessionID)
	review := s.reviews[key]
	if review == nil {
		return 0, fmt.Errorf("no active AI review")
	}
	if !samePath(review.Root, currentRoot) {
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

func snapshotWorkspace(root string) (*workspaceReview, error) {
	review := &workspaceReview{Root: root, CreatedAt: time.Now(), Files: make(map[string]reviewFile), Ignores: loadAgentGoIgnore(root)}
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

func collectReviewChanges(review *workspaceReview) ([]WorkspaceReviewChange, error) {
	current, err := scanCurrentWorkspace(review)
	if err != nil {
		return nil, err
	}
	var changes []WorkspaceReviewChange
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
func scanCurrentWorkspace(review *workspaceReview) (*workspaceReview, error) {
	current := &workspaceReview{Root: review.Root, Files: make(map[string]reviewFile)}
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

func makeReviewChange(path, status string, before, after reviewFile, reviewable bool) WorkspaceReviewChange {
	binary := before.Binary || after.Binary
	change := WorkspaceReviewChange{
		Path: path, Name: filepath.Base(path), Status: status, Reviewable: reviewable,
		Binary: binary, BeforeSize: before.Size, AfterSize: after.Size,
	}
	if !reviewable {
		change.Reason = "pre-run file exceeded the safe snapshot limit"
	}
	return change
}

func restoreReviewPath(review *workspaceReview, relPath string) error {
	_, full, err := workspaceFullPath(review.Root, relPath, false)
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
		if ignorePatternMatches(pattern, rel, name, isDir) {
			return true
		}
	}
	return false
}

func (s *AppService) BeginWorkspaceReview(sessionID string) map[string]any {
	resumed, pending, skipped, err := s.rt.workspaceReviews.begin(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "resumed": resumed, "pending_count": pending, "skipped_count": skipped}
}

func (s *AppService) ListWorkspaceReview(sessionID string) map[string]any {
	review, changes, err := s.rt.workspaceReviews.list(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if review == nil {
		return map[string]any{"success": true, "active": false, "changes": []WorkspaceReviewChange{}}
	}
	return map[string]any{
		"success": true, "active": true, "root": review.Root,
		"created_at": review.CreatedAt.Format(time.RFC3339), "skipped_count": review.Skipped,
		"changes": changes,
	}
}

func (s *AppService) WorkspaceReviewFile(sessionID, relPath string) map[string]any {
	change, before, after, err := s.rt.workspaceReviews.preview(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "change": change, "original_content": before, "modified_content": after}
}

func (s *AppService) AcceptWorkspaceReviewFile(sessionID, relPath string) map[string]any {
	remaining, err := s.rt.workspaceReviews.accept(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}

func (s *AppService) RejectWorkspaceReviewFile(sessionID, relPath string) map[string]any {
	remaining, err := s.rt.workspaceReviews.reject(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}

func (s *AppService) AcceptAllWorkspaceReview(sessionID string) map[string]any {
	s.rt.workspaceReviews.acceptAll(sessionID)
	return map[string]any{"success": true, "remaining": 0}
}

func (s *AppService) RejectAllWorkspaceReview(sessionID string) map[string]any {
	remaining, err := s.rt.workspaceReviews.rejectAll(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}
