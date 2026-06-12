package bridge

import "sync"

type pendingRun struct {
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

type pendingStore struct {
	mu   sync.Mutex
	byID map[string]pendingRun
}

func newPendingStore() *pendingStore {
	return &pendingStore{byID: make(map[string]pendingRun)}
}

func (s *pendingStore) Set(p pendingRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[p.ApprovalID] = p
}

func (s *pendingStore) Get(approvalID string) (pendingRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[approvalID]
	return p, ok
}

func (s *pendingStore) Delete(approvalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, approvalID)
}
