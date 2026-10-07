package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type collector struct {
	mu sync.Mutex
	ev []Event
}

func (c *collector) Emit(e Event) {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
}

func (c *collector) types() []EventType {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EventType, len(c.ev))
	for i, e := range c.ev {
		out[i] = e.Type
	}
	return out
}

type fakeEngine struct {
	id  string
	run func(ctx context.Context, req RunRequest, em Emitter) (RunResult, error)
}

func (f *fakeEngine) ID() string { return f.id }
func (f *fakeEngine) Run(ctx context.Context, req RunRequest, em Emitter) (RunResult, error) {
	return f.run(ctx, req, em)
}

func eq(t *testing.T, got, want []EventType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func TestRunStreamsAndFinishesWithOneDone(t *testing.T) {
	r := NewRouter()
	r.Register(&fakeEngine{id: "a", run: func(_ context.Context, _ RunRequest, em Emitter) (RunResult, error) {
		em.Token("he")
		em.Token("llo")
		em.ToolCall("1", "read", "{}")
		em.ToolResult("1", "read", "ok", false)
		return RunResult{Messages: []Message{{Role: "assistant", Type: "text", Content: "hello"}}}, nil
	}})
	c := &collector{}
	res := r.Run(context.Background(), RunRequest{SessionID: "s1", Input: "hi"}, c)
	if res.Error != "" || len(res.Messages) != 1 || res.Engine != "a" || res.RunID == "" {
		t.Fatalf("bad result: %+v", res)
	}
	eq(t, c.types(), []EventType{EventStatus, EventToken, EventToken, EventToolCall, EventToolResult, EventDone})
	var last int64
	for _, e := range c.ev {
		if e.Seq <= last || e.RunID != res.RunID || e.SessionID != "s1" || e.Engine != "a" {
			t.Fatalf("bad event %+v", e)
		}
		last = e.Seq
	}
	if len(r.Active()) != 0 {
		t.Fatal("run should be deregistered")
	}
}

func TestUnknownEngineAndEmptyRouter(t *testing.T) {
	r := NewRouter()
	c := &collector{}
	res := r.Run(context.Background(), RunRequest{}, c)
	if !contains(res.Error, ErrNoEngine.Error()) {
		t.Fatalf("want no engine error, got %q", res.Error)
	}
	eq(t, c.types(), []EventType{EventDone})

	r.Register(&fakeEngine{id: "a", run: func(context.Context, RunRequest, Emitter) (RunResult, error) { return RunResult{}, nil }})
	c = &collector{}
	res = r.Run(context.Background(), RunRequest{Engine: "zzz"}, c)
	if !contains(res.Error, ErrUnknownEngine.Error()) {
		t.Fatalf("want unknown engine error, got %q", res.Error)
	}
	if err := r.SetDefault("zzz"); !errors.Is(err, ErrUnknownEngine) {
		t.Fatalf("SetDefault err = %v", err)
	}
}

func TestDefaultEngineAndSelection(t *testing.T) {
	r := NewRouter()
	mk := func(id string) *fakeEngine {
		return &fakeEngine{id: id, run: func(context.Context, RunRequest, Emitter) (RunResult, error) { return RunResult{}, nil }}
	}
	r.Register(mk("eino"))
	r.Register(mk("coding"))
	if got := r.Run(context.Background(), RunRequest{}, nil).Engine; got != "eino" {
		t.Fatalf("default = %q", got)
	}
	if got := r.Run(context.Background(), RunRequest{Engine: "coding"}, nil).Engine; got != "coding" {
		t.Fatalf("selected = %q", got)
	}
	_ = r.SetDefault("coding")
	if got := r.Run(context.Background(), RunRequest{}, nil).Engine; got != "coding" {
		t.Fatalf("new default = %q", got)
	}
	if ids := r.Engines(); len(ids) != 2 || ids[0] != "coding" || ids[1] != "eino" {
		t.Fatalf("Engines() = %v", ids)
	}
}

func TestCancelByRunIDAndSession(t *testing.T) {
	r := NewRouter()
	started := make(chan struct{}, 2)
	r.Register(&fakeEngine{id: "a", run: func(ctx context.Context, _ RunRequest, _ Emitter) (RunResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return RunResult{}, ctx.Err() // engine surfaces ctx error
	}})

	c := &collector{}
	done := make(chan RunResult, 1)
	go func() { done <- r.Run(context.Background(), RunRequest{RunID: "r1", SessionID: "s"}, c) }()
	<-started
	if len(r.Active()) != 1 || r.Active()[0].RunID != "r1" {
		t.Fatalf("active = %+v", r.Active())
	}
	if !r.Cancel("r1") {
		t.Fatal("Cancel returned false")
	}
	res := <-done
	if res.Error != "cancelled" {
		t.Fatalf("res.Error = %q", res.Error)
	}
	eq(t, c.types(), []EventType{EventStatus, EventStatus, EventDone})
	if r.Cancel("r1") {
		t.Fatal("cancel of finished run should be false")
	}

	go func() { done <- r.Run(context.Background(), RunRequest{RunID: "r2", SessionID: "s"}, nil) }()
	go func() { done <- r.Run(context.Background(), RunRequest{RunID: "r3", SessionID: "other"}, nil) }()
	<-started
	<-started
	if n := r.CancelSession("s"); n != 1 {
		t.Fatalf("CancelSession = %d", n)
	}
	if res := <-done; res.RunID != "r2" || res.Error != "cancelled" {
		t.Fatalf("res = %+v", res)
	}
	r.Cancel("r3")
	<-done
}

func TestCallerContextCancelIsNotReportedAsCancelled(t *testing.T) {
	r := NewRouter()
	r.Register(&fakeEngine{id: "a", run: func(ctx context.Context, _ RunRequest, _ Emitter) (RunResult, error) {
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res := r.Run(ctx, RunRequest{}, nil)
	if res.Error == "cancelled" || res.Error == "" {
		t.Fatalf("res.Error = %q, want ctx error", res.Error)
	}
}

func TestEngineErrorAndPanic(t *testing.T) {
	r := NewRouter()
	r.Register(&fakeEngine{id: "err", run: func(context.Context, RunRequest, Emitter) (RunResult, error) {
		return RunResult{}, errors.New("boom")
	}})
	r.Register(&fakeEngine{id: "panic", run: func(context.Context, RunRequest, Emitter) (RunResult, error) {
		panic("kaboom")
	}})
	c := &collector{}
	res := r.Run(context.Background(), RunRequest{Engine: "err"}, c)
	if res.Error != "boom" {
		t.Fatalf("res = %+v", res)
	}
	eq(t, c.types(), []EventType{EventStatus, EventError, EventDone})

	c = &collector{}
	res = r.Run(context.Background(), RunRequest{Engine: "panic"}, c)
	if !contains(res.Error, "kaboom") {
		t.Fatalf("res = %+v", res)
	}
	eq(t, c.types(), []EventType{EventStatus, EventError, EventDone})
	if len(r.Active()) != 0 {
		t.Fatal("panicked run must be deregistered")
	}
}

func TestMultiSinkSkipsNil(t *testing.T) {
	a, b := &collector{}, &collector{}
	MultiSink(a, nil, b).Emit(Event{Type: EventToken})
	if len(a.ev) != 1 || len(b.ev) != 1 {
		t.Fatal("fan-out failed")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
