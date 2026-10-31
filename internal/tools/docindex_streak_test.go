package tools

import (
	"context"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

func freshRun(e *indexEnv) {
	e.ctx = WithEvidenceRun(governance.WithSessionID(context.Background(), "s1"))
}

// A model that keeps rewording a search that finds nothing is told from the third miss
// to stop, and after the fifth the tool refuses; one real hit starts the count over,
// and another run starts at zero.
func TestSearchTellsAModelToStopAfterRepeatedMisses(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("the launch date is in March"))
	e.add("a.txt")
	miss := func() searchDocumentsOutput { return e.search("zzzunknown qqqmissing") }

	freshRun(e)
	for i := 1; i <= 2; i++ {
		if out := miss(); strings.Contains(out.Message, "do not reword") {
			t.Fatalf("miss %d already tells the model to stop: %q", i, out.Message)
		}
	}
	if out := miss(); !strings.Contains(out.Message, "search number 3 in a row") {
		t.Fatalf("third miss: %q", out.Message)
	}
	miss()
	miss()
	_, err := e.d.search(e.ctx, searchDocumentsInput{Query: "launch date"})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("the sixth search after five misses must be refused, got %v", err)
	}

	freshRun(e)
	miss()
	miss()
	if out := e.search("launch date"); len(out.Hits) == 0 || strings.Contains(out.Message, "do not reword") {
		t.Fatalf("a real hit must not carry the stop hint: %+v", out)
	}
	for i := 1; i <= 2; i++ {
		if out := miss(); strings.Contains(out.Message, "do not reword") {
			t.Fatalf("the count must start over after a hit (miss %d): %q", i, out.Message)
		}
	}
}
