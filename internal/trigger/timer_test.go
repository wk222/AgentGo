package trigger

import (
	"testing"
	"time"
)

func TestTimerValidate(t *testing.T) {
	tr := wake("s")
	tr.Kind = KindTimer
	if err := tr.Validate(); err == nil {
		t.Fatal("a timer without fire_at must be invalid")
	}
	tr.FireAt = time.Now().Add(time.Minute).Unix()
	tr.ExpiresAt = tr.FireAt - 1
	if err := tr.Validate(); err == nil {
		t.Fatal("a timer that expires before it fires must be invalid")
	}
	tr.ExpiresAt = tr.FireAt + 60
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTimerWakesSessionOnceAfterDeadline(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)

	tr := wake("sess_t")
	tr.Kind = KindTimer
	tr.FireAt = time.Now().Add(1500*time.Millisecond).Unix() + 1 // whole seconds
	tr.ExpiresAt = tr.FireAt + 60
	created, err := e.Add(tr)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if rec.n.Load() != 0 {
		t.Fatal("fired before the deadline")
	}
	waitFor(t, "timer fire", func() bool { return rec.n.Load() == 1 })
	time.Sleep(600 * time.Millisecond)
	if rec.n.Load() != 1 {
		t.Fatalf("a timer fires exactly once, got %d", rec.n.Load())
	}
	if got, _ := s.Get(created.ID); got.Status != StatusFired || !got.Delivered {
		t.Fatalf("unexpected state: %+v", got)
	}
}

func TestPastDueTimerFiresImmediately(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)
	tr := wake("sess_p")
	tr.Kind = KindTimer
	tr.FireAt = time.Now().Add(-time.Minute).Unix()
	tr.ExpiresAt = time.Now().Add(time.Hour).Unix()
	if _, err := e.Add(tr); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "past-due fire", func() bool { return rec.n.Load() == 1 })
}

func TestCancelledTimerNeverFires(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)
	tr := wake("sess_c")
	tr.Kind = KindTimer
	tr.FireAt = time.Now().Add(2 * time.Second).Unix()
	tr.ExpiresAt = tr.FireAt + 60
	created, err := e.Add(tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Cancel(created.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if rec.n.Load() != 0 {
		t.Fatal("a cancelled timer fired")
	}
}
