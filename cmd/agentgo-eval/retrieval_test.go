package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/evaluation"
)

func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := stdout
	stdout = &buf
	t.Cleanup(func() { stdout = old })
	return &buf
}

// writeSuite writes a suite of the given queries over three short documents; every query is
// expected to find a.md. taxiText is what a.md says, so a suite can be made unable to answer.
func writeSuite(t *testing.T, path, taxiText string, queries []string) {
	t.Helper()
	s := evaluation.RetrievalSuite{
		Name: "t",
		Docs: []evaluation.CorpusDoc{
			{ID: "a.md", Text: taxiText},
			{ID: "b.md", Text: "Hotel receipts must show the name of the guest."},
			{ID: "c.md", Text: "Passwords must be changed every ninety days."},
		},
	}
	for i, q := range queries {
		s.Cases = append(s.Cases, evaluation.RetrievalCase{ID: fmt.Sprintf("q%d", i+1), Query: q, Relevant: []string{"a.md"}})
	}
	data, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func taxiQueries(n int) []string {
	var qs []string
	for i := 0; i < n; i++ {
		qs = append(qs, fmt.Sprintf("taxi rides refunded after the trip number%d", i))
	}
	return qs
}

const taxiDoc = "Taxi rides are refunded within thirty days of the trip."

func TestRetrievalRunPrintsTheSummaryAndWritesTheReport(t *testing.T) {
	out := captureStdout(t)
	dir := t.TempDir()
	writeSuite(t, filepath.Join(dir, "s.json"), taxiDoc, taxiQueries(12))
	report := filepath.Join(dir, "out", "bytes.json")
	if code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "s.json"), "-out", report}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"retrieval t [bytes]", "12 queries", "k=10", "Hit@10      1.000", "no embeddings"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	rep, err := evaluation.LoadRetrievalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Label != "bytes" || rep.Index == nil || rep.Index.Docs != 3 || rep.Hit != 1 || len(rep.Cases) != 12 {
		t.Errorf("saved report = %+v", rep)
	}
}

func TestRetrievalLabelsTheRunByChunkerAndSize(t *testing.T) {
	for _, tc := range []struct {
		chunker string
		size    int
		want    string
	}{{"", 0, "bytes"}, {"bytes", 0, "bytes"}, {"runes", 0, "runes"}, {"runes", 400, "runes@400"}} {
		if got := runLabel(tc.chunker, tc.size); got != tc.want {
			t.Errorf("runLabel(%q, %d) = %q, want %q", tc.chunker, tc.size, got, tc.want)
		}
	}
	out := captureStdout(t)
	dir := t.TempDir()
	writeSuite(t, filepath.Join(dir, "s.json"), taxiDoc, taxiQueries(3))
	if code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "s.json"), "-chunker", "runes", "-chunk-size", "400"}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "[runes@400]") {
		t.Errorf("label missing:\n%s", out)
	}
}

func TestRetrievalChunkSizeChangesHowDocumentsAreCut(t *testing.T) {
	dir := t.TempDir()
	paras := make([]string, 12)
	for i := range paras {
		paras[i] = fmt.Sprintf("Paragraph %d of the handbook explains routine office matters such as scheduling, approvals and housekeeping in some detail.", i)
	}
	s := evaluation.RetrievalSuite{
		Name:  "handbook",
		Docs:  []evaluation.CorpusDoc{{ID: "handbook.md", Text: strings.Join(paras, "\n\n")}},
		Cases: []evaluation.RetrievalCase{{ID: "q1", Query: "handbook housekeeping", Relevant: []string{"handbook.md"}}},
	}
	data, _ := json.Marshal(&s)
	suite := filepath.Join(dir, "s.json")
	if err := os.WriteFile(suite, data, 0o644); err != nil {
		t.Fatal(err)
	}
	chunks := func(extra ...string) int {
		t.Helper()
		captureStdout(t)
		report := filepath.Join(dir, fmt.Sprintf("r%d.json", len(extra)))
		args := append([]string{"run", "-suite", suite, "-out", report}, extra...)
		if code := runRetrieval(args); code != 0 {
			t.Fatalf("exit %d for %v", code, extra)
		}
		rep, err := evaluation.LoadRetrievalReport(report)
		if err != nil {
			t.Fatal(err)
		}
		return rep.Index.Chunks
	}
	whole, small := chunks(), chunks("-chunk-size", "400")
	if whole < 2 || small <= whole {
		t.Errorf("chunks: %d at the default size, %d with -chunk-size 400; a smaller size must cut the document into more pieces", whole, small)
	}
}

// brokenAndWorking returns reports of the same twelve queries: one over a corpus that can answer
// them and one over a corpus that cannot.
func brokenAndWorking(t *testing.T) (dir, working, broken string) {
	t.Helper()
	dir = t.TempDir()
	queries := taxiQueries(12)
	working, broken = filepath.Join(dir, "working.json"), filepath.Join(dir, "broken.json")
	good, bad := filepath.Join(dir, "good-suite.json"), filepath.Join(dir, "bad-suite.json")
	writeSuite(t, good, taxiDoc, queries)
	writeSuite(t, bad, "Nothing about travel is written here.", queries)
	captureStdout(t)
	if code := runRetrieval([]string{"run", "-suite", good, "-out", working}); code != 0 {
		t.Fatalf("working run: exit %d", code)
	}
	if code := runRetrieval([]string{"run", "-suite", bad, "-out", broken}); code != 0 {
		t.Fatalf("broken run: exit %d", code)
	}
	return dir, working, broken
}

func TestRetrievalCompareFailsWhenAMetricGetsWorse(t *testing.T) {
	_, working, broken := brokenAndWorking(t)
	out := captureStdout(t)
	if code := runRetrieval([]string{"compare", "-base", working, "-cand", broken}); code != 1 {
		t.Errorf("exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"REGRESSION", "Hit@10", "worse", "12 paired queries"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	out = captureStdout(t)
	if code := runRetrieval([]string{"compare", "-base", broken, "-cand", working}); code != 0 {
		t.Errorf("an improvement must not fail: exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "better") || strings.Contains(out.String(), "REGRESSION") {
		t.Errorf("want better and no regression:\n%s", out)
	}
}

func TestRetrievalRunWithBaselineGatesOnRegression(t *testing.T) {
	dir, working, _ := brokenAndWorking(t)
	out := captureStdout(t)
	code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "bad-suite.json"), "-baseline", working})
	if code != 1 || !strings.Contains(out.String(), "REGRESSION") {
		t.Errorf("exit %d, want 1 with a regression notice\n%s", code, out)
	}
	out = captureStdout(t)
	if code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "good-suite.json"), "-baseline", working}); code != 0 {
		t.Errorf("same corpus against its own baseline: exit %d\n%s", code, out)
	}
}

func TestRetrievalCompareRefusesDifferentSuites(t *testing.T) {
	dir, working, _ := brokenAndWorking(t)
	other := filepath.Join(dir, "other.json")
	writeSuite(t, filepath.Join(dir, "other-suite.json"), taxiDoc, taxiQueries(11))
	captureStdout(t)
	if code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "other-suite.json"), "-out", other}); code != 0 {
		t.Fatal("setup run failed")
	}
	if code := runRetrieval([]string{"compare", "-base", working, "-cand", other}); code != 2 {
		t.Errorf("exit %d, want 2: different numbers of queries are not comparable", code)
	}
}

func TestRetrievalUsageErrorsExitWithTwo(t *testing.T) {
	captureStdout(t)
	dir := t.TempDir()
	writeSuite(t, filepath.Join(dir, "s.json"), taxiDoc, taxiQueries(3))
	suite := filepath.Join(dir, "s.json")
	for name, args := range map[string][]string{
		"no command":        {},
		"unknown command":   {"frobnicate"},
		"run without suite": {"run"},
		"missing suite":     {"run", "-suite", filepath.Join(dir, "nope.json")},
		"unknown chunker":   {"run", "-suite", suite, "-chunker", "sentences"},
		"unknown flag":      {"run", "-suite", suite, "-bogus"},
		"zero k":            {"run", "-suite", suite, "-k", "0"},
		"missing baseline":  {"run", "-suite", suite, "-baseline", filepath.Join(dir, "nope.json")},
		"compare no files":  {"compare"},
		"compare missing":   {"compare", "-base", filepath.Join(dir, "a.json"), "-cand", filepath.Join(dir, "b.json")},
	} {
		if code := runRetrieval(args); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	out := captureStdout(t)
	if code := runRetrieval([]string{"help"}); code != 0 || !strings.Contains(out.String(), "retrieval run") {
		t.Errorf("help: exit %d, output %q", code, out)
	}
}

func TestOversizeWarning(t *testing.T) {
	rep := func(max int) *evaluation.RetrievalReport {
		return &evaluation.RetrievalReport{Index: &evaluation.IndexStats{Docs: 1, Chunks: 1, MaxChars: max}}
	}
	if got := oversizeWarning(rep(1200), 0); got != "" {
		t.Errorf("a chunk at the limit is fine, got %q", got)
	}
	if got := oversizeWarning(rep(3250), 0); !strings.Contains(got, "3250") || !strings.Contains(got, "1200") {
		t.Errorf("warning = %q", got)
	}
	if got := oversizeWarning(rep(500), 400); !strings.Contains(got, "500") || !strings.Contains(got, "400") {
		t.Errorf("a forced size is the limit that counts: %q", got)
	}
	if got := oversizeWarning(&evaluation.RetrievalReport{}, 0); got != "" {
		t.Errorf("no index stats, no warning: %q", got)
	}
}

// One unbroken Chinese paragraph is the case the built-in splitter cannot cut; the run must say so.
func TestRetrievalRunWarnsWhenTheSplitterCouldNotCut(t *testing.T) {
	out := captureStdout(t)
	dir := t.TempDir()
	long := strings.Repeat("员工应当按照流程提交申请。", 250)
	s := evaluation.RetrievalSuite{
		Name:  "long",
		Docs:  []evaluation.CorpusDoc{{ID: "long.md", Text: long}},
		Cases: []evaluation.RetrievalCase{{ID: "q1", Query: "员工提交申请", Relevant: []string{"long.md"}}},
	}
	data, _ := json.Marshal(&s)
	if err := os.WriteFile(filepath.Join(dir, "s.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runRetrieval([]string{"run", "-suite", filepath.Join(dir, "s.json")}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "WARNING: the longest chunk has 3250 characters") {
		t.Errorf("no oversize warning:\n%s", out)
	}
}
