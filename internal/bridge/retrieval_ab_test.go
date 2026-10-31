package bridge

import (
	"context"
	"flag"
	"testing"

	"agentgo/internal/docchunk"
	"agentgo/internal/evaluation"
	"agentgo/internal/memeval"
	"agentgo/internal/memory"
)

var retrievalSuite = flag.String("retrieval-suite", "", "run TestRetrievalChunkerAB on this retrieval suite JSON (see: agentgo-eval retrieval help)")

// TestRetrievalChunkerAB measures the three ways memory can cut a long document, on a suite of the
// company's own documents and questions, and prints how the two alternatives compare with what
// runs today:
//
//	bytes     the built-in splitter, which counts bytes (the default, AGENTGO_DOCCHUNK off)
//	runes     the same splitter counting characters
//	docchunk  the adaptive splitter that AGENTGO_DOCCHUNK=1 mounts, which counts characters too
//
// It lives here because docchunk may only be reached through this package, the assembly. It is a
// measurement and not a check: it fails only when a run cannot be made, and says nothing unless it
// is given a suite.
//
//	go test ./internal/bridge -run TestRetrievalChunkerAB -v -retrieval-suite path/to/suite.json
//
// Read the "index" line of each run first. For Chinese, runes and docchunk cut 1200 characters
// where bytes cuts about 400, so a difference between arms may be the size of the pieces and not
// the way they are cut; agentgo-eval retrieval run -chunk-size N isolates that. Only full-text
// recall is measured: a vector index, if one is mounted, is not part of this.
func TestRetrievalChunkerAB(t *testing.T) {
	if *retrievalSuite == "" {
		t.Skip("a measurement, not a check: give it a suite with -retrieval-suite FILE (agentgo-eval retrieval help)")
	}
	suite, err := evaluation.LoadRetrievalSuite(*retrievalSuite)
	if err != nil {
		t.Fatal(err)
	}
	runes, err := memeval.Chunker("runes", 0)
	if err != nil {
		t.Fatal(err)
	}
	arms := []struct {
		name  string
		split memory.ChunkFunc
	}{
		{"bytes", nil},
		{"runes", runes},
		{"docchunk", docchunk.NewService("").Chunk},
	}
	ctx := context.Background()
	var base *evaluation.RetrievalReport
	for _, arm := range arms {
		rep, err := memeval.Run(ctx, suite, arm.split, arm.name, evaluation.DefaultK)
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		t.Log("\n" + rep.Summary())
		if base == nil {
			base = rep
			continue
		}
		deltas, err := evaluation.CompareRetrieval(base, rep, evaluation.DefaultResamples, 1)
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		t.Log("\n" + evaluation.FormatComparison(base, rep, deltas))
	}
}
