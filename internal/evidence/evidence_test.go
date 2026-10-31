package evidence

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnapshotIdentityAndRecorderFailure(t *testing.T) {
	c := Citation{Kind: "file", Source: "a.txt", Revision: strings.Repeat("a", 64), Start: 1, End: 4, Spans: []Span{{Type: "text", Start: 0, End: 5}}}
	var saved []Record
	base := WithRecorder(context.Background(), "s", func(_ context.Context, r Record) error {
		saved = append(saved, r)
		r.Citation.Spans[0].Start = 99
		return nil
	})
	first := WithRun(base, "s")
	one := Register(first, "s", c, "中😀文")
	if one == nil || one.Spans[0].Start != 0 {
		t.Fatal("recorder mutated source")
	}
	if Register(first, "s", c, "中😀文").ID != one.ID || len(saved) != 1 {
		t.Fatal("duplicate persistence")
	}
	second := WithRun(base, "s")
	two := Register(second, "s", c, "中😀文")
	if two == nil || one.ID == two.ID || saved[0].SnapshotID != saved[1].SnapshotID {
		t.Fatal("stable snapshot/run handle separation failed")
	}
	if _, err := Read(second, "s", one.ID); err == nil {
		t.Fatal("historical handle gained eligibility")
	}
	broken := WithRun(WithRecorder(context.Background(), "s", func(context.Context, Record) error { return errors.New("disk full") }), "s")
	if Register(broken, "s", c, "中😀文") != nil {
		t.Fatal("issued citation after persistence failure")
	}
	cross := WithRun(base, "other")
	if Register(cross, "other", c, "中😀文") != nil {
		t.Fatal("recorder crossed session")
	}
}

func TestParallelRegistrationAndByteBudget(t *testing.T) {
	var mu sync.Mutex
	count := 0
	ctx := WithRun(WithRecorder(context.Background(), "s", func(context.Context, Record) error { mu.Lock(); count++; mu.Unlock(); return nil }), "s")
	c := Citation{Kind: "file", Source: "a", Revision: "r", End: 3}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if Register(ctx, "s", c, "中😀文") == nil {
				t.Error("missing citation")
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("saved %d copies", count)
	}
	c.End = 2 * 1024 * 1024
	if Register(ctx, "s", c, strings.Repeat("a", c.End)) != nil {
		t.Fatal("byte budget exceeded")
	}
}
