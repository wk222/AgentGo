package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type collectSink struct {
	mu sync.Mutex
	ev []Event
}

func (c *collectSink) Emit(e Event) {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
}

func (c *collectSink) types() []EventType {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EventType, len(c.ev))
	for i, e := range c.ev {
		out[i] = e.Type
	}
	return out
}

func (c *collectSink) count(t EventType) int {
	n := 0
	for _, x := range c.types() {
		if x == t {
			n++
		}
	}
	return n
}

type funcEngine struct {
	id  string
	run func(ctx context.Context, req RunRequest, em Emitter) (RunResult, error)
}

func (f funcEngine) ID() string { return f.id }
func (f funcEngine) Run(ctx context.Context, req RunRequest, em Emitter) (RunResult, error) {
	return f.run(ctx, req, em)
}

func TestNormalizeRequestLegacyToBlocks(t *testing.T) {
	got := NormalizeRequest(RunRequest{Input: "hi", Images: []string{"file:///a.png"}})
	if len(got.Blocks) != 2 || got.Blocks[0].Type != BlockText || got.Blocks[1].Type != BlockImage {
		t.Fatalf("blocks = %+v", got.Blocks)
	}
	if got.Input != "hi" || len(got.Images) != 1 {
		t.Fatalf("legacy fields must stay: %+v", got)
	}
}

func TestNormalizeRequestBlocksToLegacy(t *testing.T) {
	got := NormalizeRequest(RunRequest{Blocks: []ContentBlock{TextBlock("a"), ImageBlock("file:///x.png"), TextBlock("b")}})
	if got.Input != "a\nb" {
		t.Errorf("Input = %q", got.Input)
	}
	if len(got.Images) != 1 || got.Images[0] != "file:///x.png" {
		t.Errorf("Images = %v", got.Images)
	}
}

func TestNormalizeRequestBlocksAreAuthoritative(t *testing.T) {
	got := NormalizeRequest(RunRequest{Input: "legacy", Blocks: []ContentBlock{TextBlock("new")}})
	if len(got.Blocks) != 1 || got.Blocks[0].Text != "new" {
		t.Fatalf("Blocks must not be rebuilt from Input: %+v", got.Blocks)
	}
	if got.Input != "legacy" {
		t.Errorf("an explicit Input is kept, got %q", got.Input)
	}
}

func TestBlockValidation(t *testing.T) {
	card, err := CardBlock("citations", map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	ok := []ContentBlock{
		TextBlock("x"), ImageBlock("file:///a.png"), card,
		{Type: "future_kind"}, // unknown types must pass through
	}
	for _, b := range ok {
		if err := b.Validate(); err != nil {
			t.Errorf("%s should be valid: %v", b.Type, err)
		}
	}
	bad := map[string]ContentBlock{
		"no type":        {},
		"image no media": {Type: BlockImage},
		"inline data:":   {Type: BlockImage, Media: &MediaRef{URI: "data:image/png;base64,AAAA"}},
		"card no data":   {Type: BlockCard},
		"bad json":       {Type: BlockCard, Data: json.RawMessage(`{nope`)},
		"too big":        {Type: BlockText, Text: strings.Repeat("a", MaxInlineBlockBytes+1)},
	}
	for name, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRouterRejectsInvalidBlocksWithOneDone(t *testing.T) {
	r := NewRouter()
	r.Register(funcEngine{id: "e", run: func(context.Context, RunRequest, Emitter) (RunResult, error) {
		t.Error("engine must not run for an invalid request")
		return RunResult{}, nil
	}})
	s := &collectSink{}
	res := r.Run(context.Background(), RunRequest{Blocks: []ContentBlock{{Type: BlockImage}}}, s)
	if res.Error == "" || s.count(EventDone) != 1 || s.count(EventError) != 1 {
		t.Fatalf("res=%+v events=%v", res, s.types())
	}
}

func TestEmptyToolResultIsNormalizedOnce(t *testing.T) {
	r := NewRouter()
	r.Register(funcEngine{id: "e", run: func(_ context.Context, _ RunRequest, em Emitter) (RunResult, error) {
		em.ToolResult("1", "t", "   ", false)
		em.ToolResult("2", "t", "ok", false)
		return RunResult{}, nil
	}})
	s := &collectSink{}
	r.Run(context.Background(), RunRequest{Input: "x"}, s)
	var got []Event
	for _, e := range s.ev {
		if e.Type == EventToolResult {
			got = append(got, e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("tool results = %d", len(got))
	}
	if got[0].Payload["output"] != EmptyToolOutput || got[0].Payload["empty"] != true {
		t.Errorf("empty result not normalized: %+v", got[0].Payload)
	}
	if got[1].Payload["output"] != "ok" || got[1].Payload["empty"] != nil {
		t.Errorf("non-empty result touched: %+v", got[1].Payload)
	}
}

func TestBlockEventCarriesValidatedBlock(t *testing.T) {
	r := NewRouter()
	card, _ := CardBlock("citations", []string{"p1"})
	r.Register(funcEngine{id: "e", run: func(_ context.Context, _ RunRequest, em Emitter) (RunResult, error) {
		EmitBlock(em, EventCard, card)
		EmitBlock(em, EventAudioChunk, ContentBlock{Type: BlockAudio}) // invalid: no media
		EmitInputTranscript(em, "hello", true)
		return RunResult{}, nil
	}})
	s := &collectSink{}
	r.Run(context.Background(), RunRequest{Input: "x"}, s)
	if s.count(EventCard) != 1 || s.count(EventAudioChunk) != 0 || s.count(EventError) != 1 || s.count(EventInputTranscript) != 1 {
		t.Fatalf("events = %v", s.types())
	}
}

// legacyEmitter implements only the v0 Emitter: blocks must degrade to a no-op.
type legacyEmitter struct{ Emitter }

func TestBlockHelpersDegradeForLegacyEmitters(t *testing.T) {
	var em Emitter = legacyEmitter{}
	if EmitBlock(em, EventCard, TextBlock("x")) || EmitInputTranscript(em, "x", false) {
		t.Fatal("legacy emitter cannot carry blocks; helpers must report false")
	}
}

func TestInterruptSemantics(t *testing.T) {
	r := NewRouter()
	started := make(chan struct{})
	r.Register(funcEngine{id: "e", run: func(ctx context.Context, _ RunRequest, em Emitter) (RunResult, error) {
		em.Token("before")
		close(started)
		<-ctx.Done()
		// Late output after the interruption: tokens/audio must be dropped,
		// an in-flight tool result must still land.
		em.Token("after")
		EmitBlock(em, EventAudioChunk, ContentBlock{Type: BlockAudio, Media: &MediaRef{URI: "file:///a.wav"}})
		em.ToolResult("1", "t", "done", false)
		return RunResult{}, ctx.Err()
	}})
	s := &collectSink{}
	done := make(chan RunResult, 1)
	go func() { done <- r.Run(context.Background(), RunRequest{RunID: "r1", Input: "x"}, s) }()
	<-started
	if !r.Interrupt("r1", "barge-in") {
		t.Fatal("Interrupt returned false for an active run")
	}
	var res RunResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish after interrupt")
	}
	if res.Reason != "interrupted" || res.Error != "" {
		t.Errorf("result = %+v, want Reason=interrupted, no error", res)
	}
	if s.count(EventInterrupted) != 1 || s.count(EventDone) != 1 {
		t.Fatalf("events = %v", s.types())
	}
	if s.count(EventToken) != 1 || s.count(EventAudioChunk) != 0 || s.count(EventToolResult) != 1 || s.count(EventError) != 0 {
		t.Errorf("post-interrupt filtering wrong: %v", s.types())
	}
	last := s.ev[len(s.ev)-1]
	if last.Type != EventDone || last.Payload["reason"] != "interrupted" {
		t.Errorf("last event = %+v, want done with reason=interrupted", last)
	}
	for i, e := range s.ev {
		if e.Seq != int64(i+1) {
			t.Errorf("Seq not monotonic at %d: %d", i, e.Seq)
		}
	}
	if r.Interrupt("r1", "again") {
		t.Error("Interrupt of a finished run must return false")
	}
}

func TestInterruptSessionHitsOnlyThatSession(t *testing.T) {
	r := NewRouter()
	started := make(chan struct{}, 2)
	r.Register(funcEngine{id: "e", run: func(ctx context.Context, req RunRequest, em Emitter) (RunResult, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
		return RunResult{}, nil
	}})
	sa, sb := &collectSink{}, &collectSink{}
	var wg sync.WaitGroup
	for _, c := range []struct {
		sid  string
		sink *collectSink
	}{{"A", sa}, {"B", sb}} {
		wg.Add(1)
		go func() { defer wg.Done(); r.Run(context.Background(), RunRequest{SessionID: c.sid, Input: "x"}, c.sink) }()
	}
	<-started
	<-started
	if n := r.InterruptSession("A", "barge-in"); n != 1 {
		t.Fatalf("InterruptSession = %d, want 1", n)
	}
	wg.Wait()
	if sa.count(EventInterrupted) != 1 || sb.count(EventInterrupted) != 0 {
		t.Errorf("A=%v B=%v", sa.types(), sb.types())
	}
}

func TestNothingIsEmittedAfterDone(t *testing.T) {
	s := &collectSink{}
	em := newEmitter(s, "r", "", "e")
	em.Token("a")
	em.done(RunResult{})
	em.Token("late")
	em.done(RunResult{}) // second done is dropped too
	if got := s.types(); len(got) != 2 || got[1] != EventDone {
		t.Fatalf("events = %v", got)
	}
}

func TestSinkMayInterruptFromInsideEmit(t *testing.T) {
	r := NewRouter()
	r.Register(funcEngine{id: "e", run: func(ctx context.Context, _ RunRequest, em Emitter) (RunResult, error) {
		em.Token("go")
		<-ctx.Done()
		return RunResult{}, nil
	}})
	sink := SinkFunc(func(e Event) {
		if e.Type == EventToken {
			r.Interrupt(e.RunID, "from-sink") // must not deadlock
		}
	})
	done := make(chan struct{})
	go func() { r.Run(context.Background(), RunRequest{RunID: "r2", Input: "x"}, sink); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: Interrupt from a sink blocked the run")
	}
}
