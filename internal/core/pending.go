package core

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"

	"agentgo/internal/sessions"
)

type PendingRun struct {
	SessionID    string
	UserText     string
	ToolName     string
	Arguments    string
	ApprovalID   string
	InterruptID  string // Eino interrupt context id for ResumeWithData
	ResumeKind   string // "matrix" | "workflow" | "" (desktop chat)
	MatrixEvent  string
	WorkflowID   string
	CheckPointID string
}

// PendingBacking makes pending runs survive a restart. *sessions.Store
// implements it.
type PendingBacking interface {
	SavePendingRun(ctx context.Context, p sessions.PendingRun) error
	DeletePendingRun(ctx context.Context, approvalID string) error
}

// PendingStore holds the runs that are waiting for a decision. With a backing
// store every change is also written there, tagged with this process's id, so a
// restarted process can find and resume them.
type PendingStore struct {
	mu      sync.Mutex
	byID    map[string]PendingRun
	backing PendingBacking
	pid     int
}

func NewPendingStore() *PendingStore {
	return &PendingStore{byID: make(map[string]PendingRun), pid: os.Getpid()}
}

// NewDurablePendingStore is a PendingStore that persists to b.
func NewDurablePendingStore(b PendingBacking) *PendingStore {
	s := NewPendingStore()
	s.backing = b
	return s
}

func (s *PendingStore) persist(p PendingRun) {
	if s.backing == nil {
		return
	}
	b, err := json.Marshal(p)
	if err != nil {
		log.Printf("[pending] encode %s: %v", p.ApprovalID, err)
		return
	}
	if err := s.backing.SavePendingRun(context.Background(), sessions.PendingRun{
		ApprovalID: p.ApprovalID, SessionID: p.SessionID, OwnerPID: s.pid, PayloadJSON: string(b),
	}); err != nil {
		// The run stays resumable in this process; it just would not survive a restart.
		log.Printf("[pending] persist %s: %v", p.ApprovalID, err)
	}
}

func (s *PendingStore) unpersist(approvalID string) {
	if s.backing == nil {
		return
	}
	if err := s.backing.DeletePendingRun(context.Background(), approvalID); err != nil {
		log.Printf("[pending] remove %s: %v", approvalID, err)
	}
}

func (s *PendingStore) Set(p PendingRun) {
	s.mu.Lock()
	s.byID[p.ApprovalID] = p
	s.mu.Unlock()
	s.persist(p)
}

func (s *PendingStore) Get(approvalID string) (PendingRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[approvalID]
	return p, ok
}

// Take removes and returns a pending run in one step, so of any number of
// concurrent callers exactly one gets it. Resuming a run executes a tool, so the
// right to resume must be claimed atomically, not read and deleted later.
func (s *PendingStore) Take(approvalID string) (PendingRun, bool) {
	if s == nil {
		return PendingRun{}, false
	}
	s.mu.Lock()
	p, ok := s.byID[approvalID]
	if ok {
		delete(s.byID, approvalID)
	}
	s.mu.Unlock()
	if ok {
		s.unpersist(approvalID)
	}
	return p, ok
}

// Snapshot returns the pending runs held by this process.
func (s *PendingStore) Snapshot() []PendingRun {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PendingRun, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, p)
	}
	return out
}

// Len is the number of runs waiting for a decision. Nil-safe: minimal test
// runtimes have no store.
func (s *PendingStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

func (s *PendingStore) Delete(approvalID string) {
	s.mu.Lock()
	delete(s.byID, approvalID)
	s.mu.Unlock()
	s.unpersist(approvalID)
}
