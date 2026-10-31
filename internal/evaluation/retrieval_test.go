package evaluation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestScoreHitsKnownValues(t *testing.T) {
	ln3 := 1 / math.Log2(3)
	tests := []struct {
		name                  string
		hits, relevant        []string
		k                     int
		hit, recall, rr, ndcg float64
	}{
		{"answer first", []string{"A", "X"}, []string{"A"}, 5, 1, 1, 1, 1},
		{"answer second", []string{"X", "A", "Y"}, []string{"A"}, 3, 1, 1, 0.5, ln3},
		{"nothing relevant", []string{"X", "Y"}, []string{"A"}, 3, 0, 0, 0, 0},
		{"both relevant found", []string{"A", "X", "B"}, []string{"A", "B"}, 3, 1, 1, 1, (1 + 0.5) / (1 + ln3)},
		{"one of three found", []string{"A", "X"}, []string{"A", "B", "C"}, 5, 1, 1.0 / 3, 1, 1 / (1 + ln3 + 0.5)},
		// Three chunks of one document are one document found, not three.
		{"repeats of a relevant document earn nothing", []string{"A", "A", "A"}, []string{"A"}, 3, 1, 1, 1, 1},
		// Positions count chunks: three chunks of an irrelevant document push the answer to 4th.
		{"repeats of an irrelevant document push the answer down", []string{"X", "X", "X", "A"}, []string{"A"}, 4, 1, 1, 0.25, 1 / math.Log2(5)},
		{"hits beyond k are ignored", []string{"X", "A"}, []string{"A"}, 1, 0, 0, 0, 0},
		{"no hits", nil, []string{"A"}, 5, 0, 0, 0, 0},
		{"no relevant documents", []string{"A"}, nil, 5, 0, 0, 0, 0},
		{"k beyond the list", []string{"A"}, []string{"A"}, 50, 1, 1, 1, 1},
		// With k=2 only two relevant documents can fit, so finding both at the top is a perfect score.
		{"ideal is capped by k", []string{"A", "B"}, []string{"A", "B", "C"}, 2, 1, 2.0 / 3, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScoreHits(tc.hits, tc.relevant, tc.k)
			for _, c := range []struct {
				metric    string
				got, want float64
			}{{"hit", got.Hit, tc.hit}, {"recall", got.Recall, tc.recall}, {"rr", got.RR, tc.rr}, {"ndcg", got.NDCG, tc.ndcg}} {
				if !near(c.got, c.want) {
					t.Errorf("%s = %v, want %v", c.metric, c.got, c.want)
				}
			}
		})
	}
}

func TestScoreHitsKeepsOnlyTheFirstKHits(t *testing.T) {
	got := ScoreHits([]string{"X", "A", "B"}, []string{"A"}, 2)
	if len(got.Hits) != 2 || got.Hits[1] != "A" {
		t.Errorf("Hits = %v, want the first two", got.Hits)
	}
}

func canned(answers map[string][]string) SearchFunc {
	return func(_ context.Context, q string, _ int) ([]string, error) {
		hits, ok := answers[q]
		if !ok {
			return nil, fmt.Errorf("search backend is down for %q", q)
		}
		return hits, nil
	}
}

func TestRunRetrievalAveragesOverEveryCaseAndCountsFailuresAsZero(t *testing.T) {
	cases := []RetrievalCase{
		{Query: "one", Relevant: []string{"A"}},
		{Query: "two", Relevant: []string{"A"}},
		{Query: "three", Relevant: []string{"A"}},
		{ID: "broken", Query: "four", Relevant: []string{"A"}},
	}
	search := canned(map[string][]string{
		"one":   {"A"},
		"two":   {"X", "A"},
		"three": {"X"},
	})
	rep, err := RunRetrieval(context.Background(), "s", cases, 5, search)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Errors != 1 || rep.Cases[3].Error == "" {
		t.Errorf("Errors = %d, case 4 error = %q; want the failure recorded", rep.Errors, rep.Cases[3].Error)
	}
	if rep.Cases[3].ID != "broken" || rep.Cases[0].ID != "q1" {
		t.Errorf("IDs = %q, %q; want q1 filled in and broken kept", rep.Cases[0].ID, rep.Cases[3].ID)
	}
	if !near(rep.Hit, 0.5) {
		t.Errorf("Hit = %v, want 0.5 (2 of 4, the failure counting as a miss)", rep.Hit)
	}
	if !near(rep.MRR, (1+0.5)/4) {
		t.Errorf("MRR = %v, want %v", rep.MRR, (1+0.5)/4)
	}
	if rep.K != 5 || rep.Suite != "s" {
		t.Errorf("K, Suite = %d, %q", rep.K, rep.Suite)
	}
	if got := len(rep.Misses()); got != 2 {
		t.Errorf("Misses = %d, want 2 (the real miss and the failure)", got)
	}
}

func TestRunRetrievalPassesKToTheSearch(t *testing.T) {
	var got int
	search := func(_ context.Context, _ string, k int) ([]string, error) { got = k; return nil, nil }
	if _, err := RunRetrieval(context.Background(), "s", []RetrievalCase{{Query: "q", Relevant: []string{"A"}}}, 7, search); err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Errorf("search got k=%d, want 7", got)
	}
}

func TestRunRetrievalFailsWhenEverySearchFails(t *testing.T) {
	cases := []RetrievalCase{{Query: "a", Relevant: []string{"A"}}, {Query: "b", Relevant: []string{"A"}}}
	rep, err := RunRetrieval(context.Background(), "s", cases, 5, canned(nil))
	if err == nil {
		t.Fatalf("got report %+v, want an error: a run with no working search is not a result", rep)
	}
	if !strings.Contains(err.Error(), "search backend is down") {
		t.Errorf("error %q should carry the cause", err)
	}
}

func TestRunRetrievalStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	search := func(_ context.Context, _ string, _ int) ([]string, error) {
		calls++
		cancel()
		return []string{"A"}, nil
	}
	cases := []RetrievalCase{{Query: "a", Relevant: []string{"A"}}, {Query: "b", Relevant: []string{"A"}}, {Query: "c", Relevant: []string{"A"}}}
	rep, err := RunRetrieval(ctx, "s", cases, 5, search)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rep != nil || calls != 1 {
		t.Errorf("report=%v after %d calls; want no report and no further searches", rep, calls)
	}
}

func TestRunRetrievalRejectsUnusableInput(t *testing.T) {
	ok := []RetrievalCase{{Query: "q", Relevant: []string{"A"}}}
	search := canned(map[string][]string{"q": {"A"}})
	for name, call := range map[string]func() error{
		"zero k":    func() error { _, err := RunRetrieval(context.Background(), "s", ok, 0, search); return err },
		"no cases":  func() error { _, err := RunRetrieval(context.Background(), "s", nil, 5, search); return err },
		"no search": func() error { _, err := RunRetrieval(context.Background(), "s", ok, 5, nil); return err },
	} {
		if call() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// reportOf builds a report whose i-th query has reciprocal rank rr[i] (0 means it was missed).
func reportOf(label string, rr []float64) *RetrievalReport {
	r := &RetrievalReport{Suite: "s", Label: label, K: 10}
	for i, v := range rr {
		hit := 0.0
		if v > 0 {
			hit = 1
		}
		r.Cases = append(r.Cases, CaseScore{
			ID: fmt.Sprintf("q%d", i+1), Query: fmt.Sprintf("query %d", i+1),
			Hit: hit, Recall: hit, RR: v, NDCG: v,
		})
	}
	r.aggregate()
	return r
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func deltaNamed(t *testing.T, ds []MetricDelta, name string) MetricDelta {
	t.Helper()
	for _, d := range ds {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no metric %q in %+v", name, ds)
	return MetricDelta{}
}

func TestCompareRetrievalReportsAClearImprovement(t *testing.T) {
	base := reportOf("bytes", repeat(1.0/3, 40))
	cand := reportOf("runes", append(repeat(1, 30), repeat(0.5, 10)...))
	ds, err := CompareRetrieval(base, cand, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	mrr := deltaNamed(t, ds, "MRR@10")
	if mrr.Wins != 40 || mrr.Losses != 0 || mrr.Ties != 0 {
		t.Errorf("wins/losses/ties = %d/%d/%d, want 40/0/0", mrr.Wins, mrr.Losses, mrr.Ties)
	}
	wantDelta := (30*(1-1.0/3) + 10*(0.5-1.0/3)) / 40
	if !near(mrr.Delta, wantDelta) {
		t.Errorf("Delta = %v, want %v", mrr.Delta, wantDelta)
	}
	if !(mrr.Lo > 0 && mrr.Lo < mrr.Delta && mrr.Delta < mrr.Hi) {
		t.Errorf("interval [%v, %v] should sit above 0 and have some width around the mean %v", mrr.Lo, mrr.Hi, mrr.Delta)
	}
	if mrr.Verdict() != "better" {
		t.Errorf("Verdict = %q, want better", mrr.Verdict())
	}
}

func TestCompareRetrievalCallsNoiseNoDifference(t *testing.T) {
	// 6 queries improve, 5 get worse, 29 are the same: a gain of 0.025 that one more query
	// going the other way would erase.
	base := make([]float64, 40)
	cand := make([]float64, 40)
	for i := range base {
		switch {
		case i < 6:
			base[i], cand[i] = 0, 1
		case i < 11:
			base[i], cand[i] = 1, 0
		default:
			base[i], cand[i] = 1, 1
		}
	}
	ds, err := CompareRetrieval(reportOf("a", base), reportOf("b", cand), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	hit := deltaNamed(t, ds, "Hit@10")
	if !near(hit.Delta, 1.0/40) {
		t.Errorf("Delta = %v, want 0.025", hit.Delta)
	}
	if hit.Wins != 6 || hit.Losses != 5 || hit.Ties != 29 {
		t.Errorf("wins/losses/ties = %d/%d/%d, want 6/5/29", hit.Wins, hit.Losses, hit.Ties)
	}
	if !(hit.Lo < 0 && hit.Hi > 0) {
		t.Errorf("interval [%v, %v] should straddle 0", hit.Lo, hit.Hi)
	}
	if hit.Verdict() != "no clear difference" {
		t.Errorf("Verdict = %q", hit.Verdict())
	}
}

func TestCompareRetrievalSeesAClearRegression(t *testing.T) {
	ds, err := CompareRetrieval(reportOf("a", repeat(1, 30)), reportOf("b", repeat(0, 30)), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := deltaNamed(t, ds, "Hit@10"); got.Verdict() != "worse" || got.Losses != 30 {
		t.Errorf("Verdict = %q, losses = %d; want worse and 30", got.Verdict(), got.Losses)
	}
}

func TestCompareRetrievalIsRepeatable(t *testing.T) {
	a := reportOf("a", append(repeat(1, 12), repeat(0, 12)...))
	b := reportOf("b", append(repeat(0, 6), repeat(1, 18)...))
	one, _ := CompareRetrieval(a, b, 500, 7)
	two, _ := CompareRetrieval(a, b, 500, 7)
	for i := range one {
		if one[i] != two[i] {
			t.Errorf("same reports and seed gave %+v then %+v", one[i], two[i])
		}
	}
}

func TestBootstrapIntervalHasTheRightWidth(t *testing.T) {
	// Ten +1 and ten -1: mean 0, standard error 1/sqrt(20) = 0.224, so a 95% interval is about +-0.44.
	xs := append(repeat(1, 10), repeat(-1, 10)...)
	lo, hi := bootstrapMean(xs, 4000, 3)
	if lo < -0.60 || lo > -0.30 || hi > 0.60 || hi < 0.30 {
		t.Errorf("interval [%v, %v], want roughly [-0.44, +0.44]", lo, hi)
	}
	// With nothing to vary, the interval collapses onto the value.
	lo, hi = bootstrapMean(repeat(0.25, 15), 500, 3)
	if math.Abs(lo-0.25) > 1e-9 || math.Abs(hi-0.25) > 1e-9 {
		t.Errorf("interval [%v, %v] for a constant 0.25", lo, hi)
	}
}

func TestCompareRetrievalGivesNoIntervalForAHandfulOfQueries(t *testing.T) {
	a, b := reportOf("a", repeat(0, 5)), reportOf("b", repeat(1, 5))
	ds, err := CompareRetrieval(a, b, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	hit := deltaNamed(t, ds, "Hit@10")
	if !math.IsNaN(hit.Lo) || !math.IsNaN(hit.Hi) {
		t.Errorf("interval [%v, %v] from 5 queries; want none", hit.Lo, hit.Hi)
	}
	if hit.Wins != 5 || !near(hit.Delta, 1) {
		t.Errorf("the counts and the difference are still reported: %+v", hit)
	}
	if !strings.Contains(hit.Verdict(), "too few") {
		t.Errorf("Verdict = %q", hit.Verdict())
	}
	if text := FormatComparison(a, b, ds); strings.Contains(text, "NaN") {
		t.Errorf("NaN leaked into the output:\n%s", text)
	}
}

func TestCompareRetrievalRefusesToCompareUnlikeRuns(t *testing.T) {
	base := reportOf("a", repeat(1, 12))
	other := func(mutate func(*RetrievalReport)) *RetrievalReport {
		r := reportOf("b", repeat(1, 12))
		mutate(r)
		return r
	}
	for name, cand := range map[string]*RetrievalReport{
		"different k":       other(func(r *RetrievalReport) { r.K = 5 }),
		"different count":   reportOf("b", repeat(1, 11)),
		"different case id": other(func(r *RetrievalReport) { r.Cases[3].ID = "other" }),
		"different query":   other(func(r *RetrievalReport) { r.Cases[3].Query = "something else" }),
	} {
		if _, err := CompareRetrieval(base, cand, 0, 1); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := CompareRetrieval(nil, base, 0, 1); err == nil {
		t.Error("nil report: want an error")
	}
}

func TestSummaryNamesWhatItMeasuredAndWhatMissed(t *testing.T) {
	r := reportOf("runes", []float64{1, 0})
	r.Backend = "FTS only"
	r.Index = &IndexStats{Docs: 2, Chunks: 5, MeanChars: 400, MaxChars: 900}
	r.Cases[1].Query = strings.Repeat("a long question ", 10)
	r.Errors = 1
	text := r.Summary()
	for _, want := range []string{
		"[runes]", "k=10", "FTS only", "2 documents cut into 5 chunks", "longest 900",
		"Hit@10      0.500", "found nothing relevant (1)", "q2", "…", "ERRORS",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
}

func TestReportSurvivesSaveAndLoad(t *testing.T) {
	r := reportOf("runes", []float64{1, 0.5, 0})
	r.Backend = "FTS only"
	r.Index = &IndexStats{Docs: 2, Chunks: 5}
	path := filepath.Join(t.TempDir(), "nested", "run.json")
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRetrievalReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "runes" || got.K != 10 || len(got.Cases) != 3 || !near(got.MRR, r.MRR) || got.Index == nil || got.Index.Chunks != 5 {
		t.Errorf("round trip changed the report: %+v", got)
	}
}

func TestLoadRetrievalReportRejectsOtherJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	if err := writeFile(path, `{"results": []}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRetrievalReport(path); err == nil {
		t.Error("a file without cases is not a retrieval report")
	}
}
