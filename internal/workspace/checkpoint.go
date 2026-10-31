package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Per-turn workspace checkpoints.
//
// A checkpoint is the state of the tracked files at the start of one user turn:
// for every file a path, a content hash and (when it fits the budget) the bytes.
// The bytes live in a content-addressed store shared by all checkpoints of a
// session, so a turn only adds the files it changed. Rolling back to a checkpoint
// puts those files back; it never touches files the checkpoint does not know.
//
// Everything is in memory. A restart or a recycled checkpoint simply means that
// turn can no longer be rolled back; nothing else depends on the store.

// Kinds of checkpoint.
const (
	CheckpointTurn        = "turn"         // taken before a user turn
	CheckpointPreRollback = "pre_rollback" // taken just before a rollback, so the rollback can be undone
)

// checkpointRacyWindow: a file modified this close to the previous scan (or later)
// is read again instead of trusted by size+mtime, because it may have been
// rewritten within the timestamp granularity without its size changing.
const checkpointRacyWindow = 2 * time.Second

var (
	// ErrCheckpointNotFound is returned for an unknown id: it was recycled to stay
	// within the budget, or the app restarted since.
	ErrCheckpointNotFound = errors.New("checkpoint not found: it may have been recycled or the app was restarted")
	// ErrCheckpointRoot is returned when the workspace root changed since the checkpoint.
	ErrCheckpointRoot = errors.New("checkpoint belongs to another workspace")
	errTooManyFiles   = errors.New("workspace has too many files for a checkpoint")
	// ErrCheckpointPaused is returned without scanning when the last scan of this
	// workspace was too slow or found too many files: a turn must not wait for a
	// scan that is known to fail, so checkpoints pause for checkpointPause.
	ErrCheckpointPaused = errors.New("checkpoints are paused for this workspace: the last scan was too slow or the workspace has too many files")
)

const checkpointPause = 10 * time.Minute

// CheckpointLimits bounds what the store keeps and how long a scan may take.
type CheckpointLimits struct {
	MaxFileBytes    int64         // largest file whose bytes are kept; bigger files keep a hash only
	MaxSessionBytes int64         // bytes one checkpoint may reference, and what one session keeps in total
	MaxGlobalBytes  int64         // bytes kept across all sessions
	MaxPerSession   int           // checkpoints kept per session
	MaxFiles        int           // files one scan may visit
	ScanTimeout     time.Duration // 0 means no limit
}

// DefaultCheckpointLimits are the unvalidated defaults documented in
// docs/SYSTEM_DESIGN_2026-10-30.md section 1.3.
func DefaultCheckpointLimits() CheckpointLimits {
	return CheckpointLimits{
		MaxFileBytes:    maxReviewFileBytes,
		MaxSessionBytes: maxReviewTotalBytes,
		MaxGlobalBytes:  512 * 1024 * 1024,
		MaxPerSession:   30,
		MaxFiles:        200000,
		ScanTimeout:     20 * time.Second,
	}
}

func (l CheckpointLimits) withDefaults() CheckpointLimits {
	d := DefaultCheckpointLimits()
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxSessionBytes <= 0 {
		l.MaxSessionBytes = d.MaxSessionBytes
	}
	if l.MaxGlobalBytes <= 0 {
		l.MaxGlobalBytes = d.MaxGlobalBytes
	}
	if l.MaxPerSession <= 0 {
		l.MaxPerSession = d.MaxPerSession
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	return l
}

type blobKey = [sha256.Size]byte

// checkpointFile is one file in a manifest.
type checkpointFile struct {
	Hash       blobKey
	Size       int64
	ModTime    int64
	Mode       os.FileMode
	Restorable bool // the bytes are in the store
	Binary     bool
	// Opaque: the file existed but could not be read, so its content is unknown.
	// A rollback leaves such a file alone; it is never restored and never deleted.
	Opaque bool
}

type checkpoint struct {
	id        string
	sessionID string
	root      string
	kind      string
	seq       int
	prompt    string
	createdAt time.Time
	skipped   int
	ignores   []string
	files     map[string]checkpointFile
	refBytes  int64 // bytes of the distinct blobs this manifest references
	pins      int   // a rollback in progress holds the checkpoint so it is not recycled
}

// CheckpointInfo describes a checkpoint to callers (IPC, tests).
type CheckpointInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Seq       int    `json:"seq"`
	Prompt    string `json:"prompt,omitempty"`
	CreatedAt int64  `json:"created_at"` // unix milliseconds
	Files     int    `json:"files"`
	Skipped   int    `json:"skipped"`
	Root      string `json:"root"`
}

func (c *checkpoint) info() CheckpointInfo {
	return CheckpointInfo{
		ID: c.id, Kind: c.kind, Seq: c.seq, Prompt: c.prompt, CreatedAt: c.createdAt.UnixMilli(),
		Files: len(c.files), Skipped: c.skipped, Root: c.root,
	}
}

type checkpointBlob struct {
	data []byte
	refs int // manifest entries (across checkpoints) that point at this blob
}

type sessionCheckpoints struct {
	list  []*checkpoint // oldest first
	blobs map[blobKey]*checkpointBlob
	bytes int64
	seq   int
	used  time.Time
}

// CheckpointStore keeps the checkpoints of all sessions. The mutex is only held
// for bookkeeping; scanning the workspace happens outside it.
type CheckpointStore struct {
	limits   CheckpointLimits
	mu       sync.Mutex
	sessions map[string]*sessionCheckpoints
	total    int64
	paused   map[string]time.Time // workspace root -> no checkpoint attempts before this time
}

func NewCheckpointStore(limits CheckpointLimits) *CheckpointStore {
	return &CheckpointStore{
		limits:   limits.withDefaults(),
		sessions: make(map[string]*sessionCheckpoints),
		paused:   make(map[string]time.Time),
	}
}

func rootKey(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root = strings.ToLower(root)
	}
	return root
}

func (s *CheckpointStore) pausedFor(root string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rootKey(root)
	until, ok := s.paused[key]
	if ok && time.Now().Before(until) {
		return true
	}
	delete(s.paused, key)
	return false
}

func (s *CheckpointStore) pause(root string) {
	s.mu.Lock()
	s.paused[rootKey(root)] = time.Now().Add(checkpointPause)
	s.mu.Unlock()
}

func newCheckpointID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("cp_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// promptPreview is the first words of the user message, on one line.
func promptPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return text
}

func (s *CheckpointStore) sessionLocked(id string) *sessionCheckpoints {
	sc := s.sessions[id]
	if sc == nil {
		sc = &sessionCheckpoints{blobs: make(map[blobKey]*checkpointBlob)}
		s.sessions[id] = sc
	}
	sc.used = time.Now()
	return sc
}

// latestFor returns the newest checkpoint taken for the same workspace root.
func (sc *sessionCheckpoints) latestFor(root string) *checkpoint {
	for i := len(sc.list) - 1; i >= 0; i-- {
		if SamePath(sc.list[i].root, root) {
			return sc.list[i]
		}
	}
	return nil
}

func (sc *sessionCheckpoints) find(id string) *checkpoint {
	for _, c := range sc.list {
		if c.id == id {
			return c
		}
	}
	return nil
}

// Begin takes the checkpoint of a user turn. prompt is the user's message, kept
// only as a short label. On error nothing is stored; callers carry on without a
// checkpoint, because a checkpoint is a safety net and never a gate.
func (s *CheckpointStore) Begin(sessionID, root, prompt string) (CheckpointInfo, error) {
	if strings.TrimSpace(root) != "" && s.pausedFor(root) {
		return CheckpointInfo{}, ErrCheckpointPaused
	}
	cp, err := s.begin(sessionID, root, promptPreview(prompt), CheckpointTurn, nil)
	if err != nil {
		if errors.Is(err, errTooManyFiles) || errors.Is(err, context.DeadlineExceeded) {
			s.pause(root)
		}
		return CheckpointInfo{}, err
	}
	return cp.info(), nil
}

func (s *CheckpointStore) begin(sessionID, root, prompt, kind string, ignores []string) (*checkpoint, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is empty")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %s", root)
	}
	sid := reviewSessionID(sessionID)

	s.mu.Lock()
	prev := s.sessionLocked(sid).latestFor(root)
	s.mu.Unlock()
	if ignores == nil {
		// Non-nil even when empty: a checkpoint and the rollback that targets it
		// must apply exactly the same rules.
		ignores = append([]string{}, LoadIgnore(root)...)
	}

	scan, err := scanCheckpointFiles(scanRequest{
		root: root, ignores: ignores, prev: prev, store: true, limits: s.limits,
		known: func(h blobKey) (int64, bool) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if sc := s.sessions[sid]; sc != nil {
				if b := sc.blobs[h]; b != nil {
					return int64(len(b.data)), true
				}
			}
			return 0, false
		},
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.sessionLocked(sid)
	sc.seq++
	cp := &checkpoint{
		id: newCheckpointID(), sessionID: sid, root: root, kind: kind, seq: sc.seq, prompt: prompt,
		createdAt: time.Now(), skipped: scan.skipped, ignores: ignores, files: scan.files,
	}
	s.commitLocked(sc, cp, scan.pending)
	sc.list = append(sc.list, cp)
	s.enforceLocked(sc)
	return cp, nil
}

// commitLocked stores the new blobs and counts the references of cp.
func (s *CheckpointStore) commitLocked(sc *sessionCheckpoints, cp *checkpoint, pending map[blobKey][]byte) {
	for h, data := range pending {
		if sc.blobs[h] == nil {
			sc.blobs[h] = &checkpointBlob{data: data}
			sc.bytes += int64(len(data))
			s.total += int64(len(data))
		}
	}
	seen := make(map[blobKey]bool)
	for path, f := range cp.files {
		if !f.Restorable {
			continue
		}
		b := sc.blobs[f.Hash]
		if b == nil {
			// The checkpoint this entry was copied from was recycled while we scanned.
			f.Restorable = false
			cp.files[path] = f
			cp.skipped++
			continue
		}
		b.refs++
		if !seen[f.Hash] {
			seen[f.Hash] = true
			cp.refBytes += int64(len(b.data))
		}
	}
}

// dropLocked removes a checkpoint and frees the blobs nothing else points at.
func (s *CheckpointStore) dropLocked(sc *sessionCheckpoints, cp *checkpoint) {
	for i, c := range sc.list {
		if c == cp {
			sc.list = append(sc.list[:i], sc.list[i+1:]...)
			break
		}
	}
	for _, f := range cp.files {
		if !f.Restorable {
			continue
		}
		if b := sc.blobs[f.Hash]; b != nil {
			b.refs--
			if b.refs <= 0 {
				delete(sc.blobs, f.Hash)
				sc.bytes -= int64(len(b.data))
				s.total -= int64(len(b.data))
			}
		}
	}
}

// dropOldestLocked recycles the oldest checkpoint that is neither pinned nor the
// newest one. It reports whether it dropped anything.
func (s *CheckpointStore) dropOldestLocked(sc *sessionCheckpoints) bool {
	for i := 0; i < len(sc.list)-1; i++ {
		if sc.list[i].pins == 0 {
			s.dropLocked(sc, sc.list[i])
			return true
		}
	}
	return false
}

// enforceLocked brings the store back under its limits after a checkpoint was
// added to cur. The newest checkpoint of a session and pinned ones are never
// recycled for the session's own limits.
func (s *CheckpointStore) enforceLocked(cur *sessionCheckpoints) {
	for len(cur.list) > s.limits.MaxPerSession && s.dropOldestLocked(cur) {
	}
	for cur.bytes > s.limits.MaxSessionBytes && s.dropOldestLocked(cur) {
	}
	for s.total > s.limits.MaxGlobalBytes {
		var victim *sessionCheckpoints
		for _, sc := range s.sessions {
			if sc == cur || len(sc.list) == 0 {
				continue
			}
			if victim == nil || sc.used.Before(victim.used) {
				victim = sc
			}
		}
		switch {
		case victim != nil && s.dropOldestLocked(victim):
		case victim != nil && len(victim.list) > 0 && victim.list[0].pins == 0:
			s.dropLocked(victim, victim.list[0]) // the newest one of an idle session
		case s.dropOldestLocked(cur):
		default:
			return
		}
	}
}

// List returns the checkpoints of a session, oldest first.
func (s *CheckpointStore) List(sessionID string) []CheckpointInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.sessions[reviewSessionID(sessionID)]
	if sc == nil {
		return []CheckpointInfo{}
	}
	out := make([]CheckpointInfo, 0, len(sc.list))
	for _, c := range sc.list {
		out = append(out, c.info())
	}
	return out
}

// Get returns one checkpoint of a session.
func (s *CheckpointStore) Get(sessionID, id string) (CheckpointInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc := s.sessions[reviewSessionID(sessionID)]; sc != nil {
		if c := sc.find(id); c != nil {
			return c.info(), true
		}
	}
	return CheckpointInfo{}, false
}

// Drop forgets all checkpoints of a session (it was deleted).
func (s *CheckpointStore) Drop(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := reviewSessionID(sessionID)
	if sc := s.sessions[id]; sc != nil {
		s.total -= sc.bytes
		delete(s.sessions, id)
	}
}

// Clear forgets everything (the workspace changed).
func (s *CheckpointStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*sessionCheckpoints)
	s.total = 0
	s.paused = make(map[string]time.Time)
}

// bytesHeld reports how many bytes the store keeps for a session (tests, diagnostics).
func (s *CheckpointStore) bytesHeld(sessionID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc := s.sessions[reviewSessionID(sessionID)]; sc != nil {
		return sc.bytes
	}
	return 0
}
