package audio

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestControlInterruptForwardsAndPublishes(t *testing.T) {
	var gotSession, gotReason string
	var published []Event
	c := NewControl("s1",
		func(sid, reason string) bool { gotSession, gotReason = sid, reason; return true },
		func(e Event) { published = append(published, e) }, nil)

	if err := c.Interrupt("barge-in"); err != nil {
		t.Fatal(err)
	}
	if gotSession != "s1" || gotReason != "barge-in" {
		t.Errorf("interrupter got (%q,%q)", gotSession, gotReason)
	}
	if len(published) != 1 || published[0].Kind != EventInterrupted || published[0].Reason != "barge-in" {
		t.Errorf("published = %+v", published)
	}
}

func TestControlDefaultsReasonAndCloseIsIdempotent(t *testing.T) {
	var reason string
	var closed atomic.Int32
	c := NewControl("s", func(_, r string) bool { reason = r; return false }, nil, func() { closed.Add(1) })
	_ = c.Interrupt("")
	if reason != "user" {
		t.Errorf("default reason = %q", reason)
	}
	_ = c.Close()
	_ = c.Close()
	if closed.Load() != 1 {
		t.Errorf("onClose ran %d times, want 1", closed.Load())
	}
	if err := c.Interrupt("x"); !errors.Is(err, ErrClosed) {
		t.Errorf("Interrupt after Close = %v, want ErrClosed", err)
	}
}
