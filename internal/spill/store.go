package spill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("spill: record not found")
	ErrInvalidSpillID = errors.New("spill: invalid spill id")
)

// FileSpillStore implements Store backed by the local filesystem.
type FileSpillStore struct {
	baseDir string
	mu      sync.RWMutex
	records map[string]*SpillRecord
}

// NewFileSpillStore creates a file-backed spill storage in the specified base directory.
func NewFileSpillStore(baseDir string) (*FileSpillStore, error) {
	cleanDir := filepath.Clean(baseDir)
	if err := os.MkdirAll(cleanDir, 0o755); err != nil {
		return nil, fmt.Errorf("spill: create base dir failed: %w", err)
	}
	s := &FileSpillStore{
		baseDir: cleanDir,
		records: make(map[string]*SpillRecord),
	}
	_ = s.loadExistingMeta()
	return s, nil
}

func (s *FileSpillStore) loadExistingMeta() error {
	metaFile := filepath.Join(s.baseDir, "index.json")
	data, err := os.ReadFile(metaFile)
	if err != nil {
		return nil
	}
	var loaded map[string]*SpillRecord
	if err := json.Unmarshal(data, &loaded); err == nil && loaded != nil {
		s.records = loaded
	}
	return nil
}

func (s *FileSpillStore) persistMetaLocked() {
	metaFile := filepath.Join(s.baseDir, "index.json")
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err == nil {
		_ = os.WriteFile(metaFile, data, 0o644)
	}
}

// Save writes raw content to a spill file and returns metadata.
func (s *FileSpillStore) Save(ctx context.Context, sessionID, toolCallID, toolName string, rawContent []byte) (*SpillRecord, error) {
	if len(rawContent) == 0 {
		return nil, errors.New("spill: content is empty")
	}
	if sessionID == "" {
		sessionID = "default"
	}

	id := "spill_" + strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	sessionDir := filepath.Join(s.baseDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}

	targetPath := filepath.Join(sessionDir, id+".raw")
	if err := os.WriteFile(targetPath, rawContent, 0o644); err != nil {
		return nil, fmt.Errorf("spill: write file failed: %w", err)
	}

	lines := bytes.Count(rawContent, []byte("\n"))
	if len(rawContent) > 0 && !bytes.HasSuffix(rawContent, []byte("\n")) {
		lines++
	}

	record := &SpillRecord{
		ID:         id,
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		TotalBytes: int64(len(rawContent)),
		TotalLines: lines,
		FilePath:   targetPath,
		CreatedAt:  time.Now().UTC(),
	}

	s.mu.Lock()
	s.records[id] = record
	s.persistMetaLocked()
	s.mu.Unlock()

	return record, nil
}

// Fetch reads a sliced and filtered segment of a spilled output.
func (s *FileSpillStore) Fetch(ctx context.Context, spillID string, opts FetchOptions) (*FetchResult, error) {
	s.mu.RLock()
	record, ok := s.records[spillID]
	s.mu.RUnlock()

	if !ok || record == nil {
		return nil, ErrNotFound
	}

	file, err := os.Open(record.FilePath)
	if err != nil {
		return nil, fmt.Errorf("spill: open file failed: %w", err)
	}
	defer file.Close()

	var filterRegex *regexp.Regexp
	if opts.RegexFilter != "" {
		var err error
		filterRegex, err = regexp.Compile(opts.RegexFilter)
		if err != nil {
			return nil, fmt.Errorf("spill: invalid regex filter: %w", err)
		}
	}

	offset := opts.OffsetLine
	if offset <= 0 {
		offset = 1
	}
	limit := opts.LimitLines
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	scanner := bufio.NewScanner(file)
	// Support long lines up to 1MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var matchedLines []string
	currentLine := 0
	hasMore := false

	for scanner.Scan() {
		currentLine++
		text := scanner.Text()

		if filterRegex != nil && !filterRegex.MatchString(text) {
			continue
		}

		if currentLine < offset {
			continue
		}

		if len(matchedLines) < limit {
			matchedLines = append(matchedLines, fmt.Sprintf("%6d | %s", currentLine, text))
		} else {
			hasMore = true
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("spill: scan error: %w", err)
	}

	return &FetchResult{
		ID:            record.ID,
		TotalLines:    record.TotalLines,
		TotalBytes:    record.TotalBytes,
		OffsetLine:    offset,
		ReturnedLines: len(matchedLines),
		HasMore:       hasMore,
		Content:       strings.Join(matchedLines, "\n"),
	}, nil
}

// GetRecord returns record metadata by id.
func (s *FileSpillStore) GetRecord(ctx context.Context, spillID string) (*SpillRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[spillID]
	if !ok {
		return nil, ErrNotFound
	}
	return record, nil
}

// Delete removes a single spill record and file.
func (s *FileSpillStore) Delete(ctx context.Context, spillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[spillID]
	if !ok {
		return nil
	}
	_ = os.Remove(record.FilePath)
	delete(s.records, spillID)
	s.persistMetaLocked()
	return nil
}

// CleanupSession removes all spills belonging to a session.
func (s *FileSpillStore) CleanupSession(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionDir := filepath.Join(s.baseDir, sessionID)
	_ = os.RemoveAll(sessionDir)
	for id, r := range s.records {
		if r.SessionID == sessionID {
			delete(s.records, id)
		}
	}
	s.persistMetaLocked()
	return nil
}
