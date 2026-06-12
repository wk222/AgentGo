package workflow

import (
	"context"
	"testing"
	"time"
)

func TestLeaseAcquireRelease(t *testing.T) {
	s := NewLeaseStore()
	if !s.Acquire("k1", "a", time.Minute) {
		t.Fatal("acquire a")
	}
	if s.Acquire("k1", "b", time.Minute) {
		t.Fatal("b should wait")
	}
	s.Release("k1", "a")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.WaitAcquire(ctx, "k1", "b", time.Minute, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
