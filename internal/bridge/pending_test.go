package bridge

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Resuming a run executes a tool, so of many concurrent callers exactly one may
// get the pending run.
func TestPendingTakeHandsTheRunToExactlyOneCaller(t *testing.T) {
	s := newPendingStore()
	s.Set(pendingRun{ApprovalID: "a1", ToolName: "code_edit"})

	var wg sync.WaitGroup
	var got atomic.Int32
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, ok := s.Take("a1"); ok && p.ToolName == "code_edit" {
				got.Add(1)
			}
		}()
	}
	wg.Wait()
	if got.Load() != 1 {
		t.Fatalf("%d callers got the run, want 1", got.Load())
	}
	if _, ok := s.Get("a1"); ok || s.Len() != 0 {
		t.Fatal("a taken run must be gone")
	}
	if _, ok := (*pendingStore)(nil).Take("a1"); ok {
		t.Fatal("nil store must be safe")
	}
}
