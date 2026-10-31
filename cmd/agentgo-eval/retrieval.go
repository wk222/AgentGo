package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"agentgo/internal/evaluation"
	"agentgo/internal/memeval"
)

const retrievalUsage = `agentgo-eval retrieval: does a search put the right document in front of the model?

  agentgo-eval retrieval run     -suite FILE [-k 10] [-chunker bytes|runes] [-chunk-size N]
                                 [-label NAME] [-out FILE] [-baseline FILE]
  agentgo-eval retrieval compare -base FILE -cand FILE

run indexes the suite's documents through the real memory engine in a throw-away in-memory
database, asks the suite's questions, and reports Hit@k, Recall@k, MRR@k and nDCG@k per document.
It needs no running agentgo and no network. It measures full-text recall only: there are no
embeddings, no Milvus and no LLM query rewrite, so it says nothing about vector search.

The suite is JSON: a corpus (inline "docs", or a "docs_dir" of .md/.txt files in UTF-8 or GBK)
and "cases", each a "query" and the "relevant" document IDs.

To compare two ways of cutting documents, run each with -out and compare the two files, or give
run a -baseline. The comparison reports a 95% bootstrap interval for every metric and only calls a
difference real when that interval leaves zero out. Exit code: 0 ok, 1 a metric got worse, 2 error.
`

// bootstrapSeed fixes the resampling, so the same two reports always give the same intervals.
const bootstrapSeed = 1

// ingestChunkSize is what memory's ingest asks every chunker for.
const ingestChunkSize = 1200

// stdout is where reports go; tests replace it.
var stdout io.Writer = os.Stdout

func runRetrieval(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, retrievalUsage)
		return 2
	}
	switch args[0] {
	case "run":
		return retrievalRun(args[1:])
	case "compare":
		return retrievalCompare(args[1:])
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, retrievalUsage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "agentgo-eval retrieval: unknown command %q\n\n%s", args[0], retrievalUsage)
	return 2
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// parseFlags returns a process exit code, or -1 to carry on.
func parseFlags(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	return -1
}

func retrievalRun(args []string) int {
	fs := newFlagSet("retrieval run")
	var (
		suitePath = fs.String("suite", "", "suite JSON (required)")
		k         = fs.Int("k", evaluation.DefaultK, "hits kept per query")
		chunker   = fs.String("chunker", "bytes", "how long documents are cut: bytes (what runs today) or runes")
		size      = fs.Int("chunk-size", 0, "force this chunk size instead of the 1200 that ingest asks for (0 keeps it)")
		label     = fs.String("label", "", "name of this run in reports (default: the chunker)")
		out       = fs.String("out", "", "write the report JSON here")
		baseline  = fs.String("baseline", "", "compare with this report JSON; a worse metric => exit 1")
		resamples = fs.Int("resamples", evaluation.DefaultResamples, "bootstrap resamples for -baseline")
		timeout   = fs.Duration("timeout", 10*time.Minute, "give up after this long")
	)
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *suitePath == "" {
		fs.Usage()
		return fail("retrieval run: -suite is required")
	}
	suite, err := evaluation.LoadRetrievalSuite(*suitePath)
	if err != nil {
		return fail("suite: %v", err)
	}
	split, err := memeval.Chunker(*chunker, *size)
	if err != nil {
		return fail("%v", err)
	}
	name := *label
	if name == "" {
		name = runLabel(*chunker, *size)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	rep, err := memeval.Run(ctx, suite, split, name, *k)
	if err != nil {
		return fail("run: %v", err)
	}
	fmt.Fprint(stdout, rep.Summary())
	if warn := oversizeWarning(rep, *size); warn != "" {
		fmt.Fprint(stdout, warn)
	}
	if *out != "" {
		if err := rep.Save(*out); err != nil {
			return fail("write %s: %v", *out, err)
		}
		fmt.Fprintf(stdout, "report written to %s\n", *out)
	}
	if *baseline == "" {
		return 0
	}
	base, err := evaluation.LoadRetrievalReport(*baseline)
	if err != nil {
		return fail("baseline: %v", err)
	}
	return printComparison(stdout, base, rep, *resamples)
}

func runLabel(chunker string, size int) string {
	if chunker == "" {
		chunker = "bytes"
	}
	if size > 0 {
		return fmt.Sprintf("%s@%d", chunker, size)
	}
	return chunker
}

// oversizeWarning notes chunks longer than the size they were cut to. The built-in splitter cuts
// at blank lines, and at spaces and full stops followed by a space; text without them, such as a
// long Chinese paragraph, comes out as one chunk however long it is.
func oversizeWarning(rep *evaluation.RetrievalReport, size int) string {
	limit := ingestChunkSize
	if size > 0 {
		limit = size
	}
	x := rep.Index
	if x == nil || x.MaxChars <= limit {
		return ""
	}
	return fmt.Sprintf("  WARNING: the longest chunk has %d characters but chunks were to be at most %d: some text could not be cut\n"+
		"           (long paragraphs without blank lines or spaces, typically Chinese). Those documents are searched as one piece.\n",
		x.MaxChars, limit)
}

func retrievalCompare(args []string) int {
	fs := newFlagSet("retrieval compare")
	var (
		basePath  = fs.String("base", "", "the earlier report JSON (required)")
		candPath  = fs.String("cand", "", "the report JSON to judge against it (required)")
		resamples = fs.Int("resamples", evaluation.DefaultResamples, "bootstrap resamples")
	)
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *basePath == "" || *candPath == "" {
		fs.Usage()
		return fail("retrieval compare: -base and -cand are required")
	}
	base, err := evaluation.LoadRetrievalReport(*basePath)
	if err != nil {
		return fail("base: %v", err)
	}
	cand, err := evaluation.LoadRetrievalReport(*candPath)
	if err != nil {
		return fail("cand: %v", err)
	}
	return printComparison(stdout, base, cand, *resamples)
}

// printComparison prints how cand compares with base and returns the exit code: 1 when any
// metric got clearly worse.
func printComparison(w io.Writer, base, cand *evaluation.RetrievalReport, resamples int) int {
	deltas, err := evaluation.CompareRetrieval(base, cand, resamples, bootstrapSeed)
	if err != nil {
		return fail("compare: %v", err)
	}
	fmt.Fprint(w, "\n", evaluation.FormatComparison(base, cand, deltas))
	var worse []string
	for _, d := range deltas {
		if d.Verdict() == "worse" {
			worse = append(worse, d.Name)
		}
	}
	if len(worse) > 0 {
		fmt.Fprintf(w, "REGRESSION: %s got worse\n", strings.Join(worse, ", "))
		return 1
	}
	return 0
}
