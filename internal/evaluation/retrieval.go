package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

// Retrieval evaluation asks one question of a search: for a query a person would really type,
// does the document that answers it come back near the top?
//
// The unit of judgement is the document, not the chunk. A chunker decides how a document is cut,
// so scoring chunks would make every chunker look best on its own cuts; scoring documents lets a
// 1200-byte chunker and a 512-character one be compared on the same suite. A search therefore
// reports each hit as the ID of the document it was cut from. A document that shows up through
// several chunks fills several of the k slots and earns nothing for the repeats: that is the
// price a context window pays for redundant chunks, and the metrics charge it.
//
// Relevance is binary (a document answers the query or it does not), so the metrics are:
//
//	Hit@k     1 if any relevant document is among the k hits
//	Recall@k  the share of the relevant documents found
//	MRR@k     1 / (position of the first relevant hit), 0 if none; positions count chunks
//	nDCG@k    relevant documents found early, each counted once, against the best possible order
//
// Each is averaged over the queries. A query whose search failed counts as zero and is reported
// in Errors, so a broken backend cannot look good by skipping its failures.

// DefaultK is how many hits a retrieval run keeps. It sits just under the 12 that the memory
// context pack asks for, so the numbers describe what the model is actually shown.
const DefaultK = 10

// RetrievalCase is one question and the documents that answer it.
type RetrievalCase struct {
	ID       string   `json:"id,omitempty"`
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
}

// SearchFunc runs one query and returns the source document of each of the (at most k) best hits,
// best first. A document repeats when several of its chunks are among the hits.
type SearchFunc func(ctx context.Context, query string, k int) ([]string, error)

// CaseScore is the outcome of one query. Hit, Recall, RR and NDCG are in [0,1].
type CaseScore struct {
	ID     string   `json:"id"`
	Query  string   `json:"query"`
	Hits   []string `json:"hits,omitempty"`
	Hit    float64  `json:"hit"`
	Recall float64  `json:"recall"`
	RR     float64  `json:"rr"`
	NDCG   float64  `json:"ndcg"`
	Millis float64  `json:"ms"`
	Error  string   `json:"error,omitempty"`
}

// IndexStats says how a backend cut the corpus. Zero means the backend did not report it.
type IndexStats struct {
	Docs      int `json:"docs"`
	Chunks    int `json:"chunks"`
	MeanChars int `json:"mean_chars"`
	MaxChars  int `json:"max_chars"`
}

// RetrievalReport is the result of one run. The aggregate fields are means over every case.
type RetrievalReport struct {
	Suite string `json:"suite"`
	// Label names the arm of an A/B comparison, for example the chunker that was used.
	Label string `json:"label,omitempty"`
	// Backend says what actually answered the queries, so a number is never read as more than it is.
	Backend string      `json:"backend,omitempty"`
	K       int         `json:"k"`
	Index   *IndexStats `json:"index,omitempty"`
	Cases   []CaseScore `json:"cases"`

	Hit        float64 `json:"hit"`
	Recall     float64 `json:"recall"`
	MRR        float64 `json:"mrr"`
	NDCG       float64 `json:"ndcg"`
	MeanMillis float64 `json:"mean_ms"`
	Errors     int     `json:"errors"`
}

// ScoreHits scores one ranked list of hit documents against the relevant set. Only the first k
// hits count; k <= 0 means all of them.
func ScoreHits(hits, relevant []string, k int) CaseScore {
	want := make(map[string]bool, len(relevant))
	for _, id := range relevant {
		want[id] = true
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	s := CaseScore{Hits: append([]string(nil), hits...)}

	found := make(map[string]bool, len(want))
	var dcg float64
	for i, id := range hits {
		if !want[id] || found[id] {
			continue
		}
		found[id] = true
		dcg += discount(i)
		if s.RR == 0 {
			s.RR = 1 / float64(i+1)
		}
	}
	if len(found) > 0 {
		s.Hit = 1
	}
	if n := len(want); n > 0 {
		s.Recall = float64(len(found)) / float64(n)
		slots := n
		if k > 0 && k < slots {
			slots = k
		}
		var ideal float64
		for i := 0; i < slots; i++ {
			ideal += discount(i)
		}
		if ideal > 0 {
			s.NDCG = dcg / ideal
		}
	}
	return s
}

// discount is the weight of rank position i (0-based) in DCG.
func discount(i int) float64 { return 1 / math.Log2(float64(i+2)) }

// RunRetrieval runs every case through search and scores the answers. It fails only when the
// input is unusable, the context ends, or every single search failed; an individual failure is
// recorded in its CaseScore and counted in Errors.
func RunRetrieval(ctx context.Context, suite string, cases []RetrievalCase, k int, search SearchFunc) (*RetrievalReport, error) {
	if k <= 0 {
		return nil, fmt.Errorf("k must be positive, got %d", k)
	}
	if len(cases) == 0 {
		return nil, errors.New("no cases to run")
	}
	if search == nil {
		return nil, errors.New("no search function")
	}
	rep := &RetrievalReport{Suite: suite, K: k, Cases: make([]CaseScore, 0, len(cases))}
	var firstErr error
	for i, c := range cases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		started := time.Now()
		hits, err := search(ctx, c.Query, k)
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("case %s: %w", caseID(c, i), err)
			}
			rep.Cases = append(rep.Cases, CaseScore{ID: caseID(c, i), Query: c.Query, Millis: elapsed, Error: err.Error()})
			rep.Errors++
			continue
		}
		s := ScoreHits(hits, c.Relevant, k)
		s.ID, s.Query, s.Millis = caseID(c, i), c.Query, elapsed
		rep.Cases = append(rep.Cases, s)
	}
	if rep.Errors == len(cases) {
		return nil, fmt.Errorf("every search failed; the first error was: %w", firstErr)
	}
	rep.aggregate()
	return rep, nil
}

func caseID(c RetrievalCase, i int) string {
	if c.ID != "" {
		return c.ID
	}
	return fmt.Sprintf("q%d", i+1)
}

func (r *RetrievalReport) aggregate() {
	n := float64(len(r.Cases))
	if n == 0 {
		return
	}
	r.Hit, r.Recall, r.MRR, r.NDCG, r.MeanMillis = 0, 0, 0, 0, 0
	for _, c := range r.Cases {
		r.Hit += c.Hit
		r.Recall += c.Recall
		r.MRR += c.RR
		r.NDCG += c.NDCG
		r.MeanMillis += c.Millis
	}
	r.Hit /= n
	r.Recall /= n
	r.MRR /= n
	r.NDCG /= n
	r.MeanMillis /= n
}

// Misses returns the cases that found no relevant document, in suite order.
func (r *RetrievalReport) Misses() []CaseScore {
	var out []CaseScore
	for _, c := range r.Cases {
		if c.Hit == 0 {
			out = append(out, c)
		}
	}
	return out
}

// Summary renders the report for a terminal.
func (r *RetrievalReport) Summary() string {
	var b strings.Builder
	title := r.Suite
	if r.Label != "" {
		title += " [" + r.Label + "]"
	}
	fmt.Fprintf(&b, "retrieval %s: %d queries, k=%d\n", title, len(r.Cases), r.K)
	if r.Backend != "" {
		fmt.Fprintf(&b, "  backend    %s\n", r.Backend)
	}
	if x := r.Index; x != nil && x.Chunks > 0 {
		fmt.Fprintf(&b, "  index      %d documents cut into %d chunks (mean %d, longest %d characters)\n",
			x.Docs, x.Chunks, x.MeanChars, x.MaxChars)
	}
	fmt.Fprintf(&b, "  Hit@%d      %.3f   queries with a relevant document in the top %d\n", r.K, r.Hit, r.K)
	fmt.Fprintf(&b, "  Recall@%d   %.3f   share of the relevant documents found\n", r.K, r.Recall)
	fmt.Fprintf(&b, "  MRR@%d      %.3f   1 / position of the first relevant hit\n", r.K, r.MRR)
	fmt.Fprintf(&b, "  nDCG@%d     %.3f   relevant documents found early, each counted once\n", r.K, r.NDCG)
	fmt.Fprintf(&b, "  latency    %.1f ms per query\n", r.MeanMillis)
	if r.Errors > 0 {
		fmt.Fprintf(&b, "  ERRORS     %d of %d searches failed and count as zero\n", r.Errors, len(r.Cases))
	}
	const listed = 10
	miss := r.Misses()
	for i, c := range miss {
		if i == listed {
			fmt.Fprintf(&b, "  ... and %d more\n", len(miss)-listed)
			break
		}
		if i == 0 {
			fmt.Fprintf(&b, "  found nothing relevant (%d):\n", len(miss))
		}
		fmt.Fprintf(&b, "    %s  %s\n", c.ID, clip(c.Query, 60))
	}
	return b.String()
}

// clip shortens s to at most n characters.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Save writes the report as indented JSON, creating the directory if needed.
func (r *RetrievalReport) Save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadRetrievalReport reads a report written by Save.
func LoadRetrievalReport(path string) (*RetrievalReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r RetrievalReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(r.Cases) == 0 {
		return nil, fmt.Errorf("%s: not a retrieval report (no cases)", path)
	}
	return &r, nil
}

// MetricDelta is the difference of one metric between two runs on the same queries.
type MetricDelta struct {
	Name       string
	Base, Cand float64
	Delta      float64
	// Lo and Hi bound the 95% bootstrap interval of the mean per-query difference. They are NaN
	// when there are too few queries to say anything.
	Lo, Hi             float64
	Wins, Losses, Ties int
}

// MinPairsForInterval is the fewest queries for which an interval is reported. Below it a
// bootstrap only restates the handful of numbers it was given.
const MinPairsForInterval = 10

// DefaultResamples is the number of bootstrap resamples.
const DefaultResamples = 2000

// Verdict reads the interval: a difference only counts when the interval leaves zero out.
func (d MetricDelta) Verdict() string {
	switch {
	case math.IsNaN(d.Lo) || math.IsNaN(d.Hi):
		return "too few queries for an interval"
	case d.Lo > 0:
		return "better"
	case d.Hi < 0:
		return "worse"
	default:
		return "no clear difference"
	}
}

// CompareRetrieval compares two runs of the same suite, query by query. The runs must have the
// same k and the same queries in the same order; anything else would compare unlike things.
// Intervals come from a paired bootstrap over the per-query differences; seed fixes the resampling
// so the same two reports always give the same intervals.
func CompareRetrieval(base, cand *RetrievalReport, resamples int, seed int64) ([]MetricDelta, error) {
	if base == nil || cand == nil {
		return nil, errors.New("need two reports")
	}
	if base.K != cand.K {
		return nil, fmt.Errorf("the runs used different k (%d and %d)", base.K, cand.K)
	}
	if len(base.Cases) != len(cand.Cases) {
		return nil, fmt.Errorf("the runs have different numbers of queries (%d and %d)", len(base.Cases), len(cand.Cases))
	}
	for i := range base.Cases {
		b, c := base.Cases[i], cand.Cases[i]
		if b.ID != c.ID || b.Query != c.Query {
			return nil, fmt.Errorf("query %d differs between the runs (%q vs %q): they are not the same suite", i+1, b.ID, c.ID)
		}
	}
	if resamples <= 0 {
		resamples = DefaultResamples
	}
	metrics := []struct {
		name string
		of   func(CaseScore) float64
	}{
		{fmt.Sprintf("Hit@%d", base.K), func(c CaseScore) float64 { return c.Hit }},
		{fmt.Sprintf("Recall@%d", base.K), func(c CaseScore) float64 { return c.Recall }},
		{fmt.Sprintf("MRR@%d", base.K), func(c CaseScore) float64 { return c.RR }},
		{fmt.Sprintf("nDCG@%d", base.K), func(c CaseScore) float64 { return c.NDCG }},
	}
	out := make([]MetricDelta, 0, len(metrics))
	for _, m := range metrics {
		deltas := make([]float64, len(base.Cases))
		d := MetricDelta{Name: m.name, Lo: math.NaN(), Hi: math.NaN()}
		for i := range base.Cases {
			bv, cv := m.of(base.Cases[i]), m.of(cand.Cases[i])
			deltas[i] = cv - bv
			d.Base += bv
			d.Cand += cv
			switch {
			case deltas[i] > 1e-9:
				d.Wins++
			case deltas[i] < -1e-9:
				d.Losses++
			default:
				d.Ties++
			}
		}
		n := float64(len(deltas))
		d.Base /= n
		d.Cand /= n
		d.Delta = d.Cand - d.Base
		if len(deltas) >= MinPairsForInterval {
			d.Lo, d.Hi = bootstrapMean(deltas, resamples, seed)
		}
		out = append(out, d)
	}
	return out, nil
}

// bootstrapMean returns the 2.5% and 97.5% quantiles of the mean of xs over resamples draws with
// replacement.
func bootstrapMean(xs []float64, resamples int, seed int64) (lo, hi float64) {
	rng := rand.New(rand.NewSource(seed))
	n := len(xs)
	means := make([]float64, resamples)
	for r := range means {
		var sum float64
		for i := 0; i < n; i++ {
			sum += xs[rng.Intn(n)]
		}
		means[r] = sum / float64(n)
	}
	sort.Float64s(means)
	loIdx := int(math.Floor(0.025 * float64(resamples)))
	hiIdx := int(math.Ceil(0.975*float64(resamples))) - 1
	return means[loIdx], means[min(hiIdx, resamples-1)]
}

// FormatComparison renders a comparison for a terminal.
func FormatComparison(base, cand *RetrievalReport, deltas []MetricDelta) string {
	var b strings.Builder
	name := func(r *RetrievalReport, fallback string) string {
		if r.Label != "" {
			return r.Label
		}
		return fallback
	}
	fmt.Fprintf(&b, "%s -> %s on %q: %d paired queries, k=%d, 95%% bootstrap interval of the change\n",
		name(base, "base"), name(cand, "candidate"), base.Suite, len(base.Cases), base.K)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  metric\tbase\tcandidate\tchange\tinterval\twins/losses/ties\tverdict\n")
	for _, d := range deltas {
		interval := "-"
		if !math.IsNaN(d.Lo) && !math.IsNaN(d.Hi) {
			interval = fmt.Sprintf("[%+.3f, %+.3f]", d.Lo, d.Hi)
		}
		fmt.Fprintf(tw, "  %s\t%.3f\t%.3f\t%+.3f\t%s\t%d/%d/%d\t%s\n",
			d.Name, d.Base, d.Cand, d.Delta, interval, d.Wins, d.Losses, d.Ties, d.Verdict())
	}
	_ = tw.Flush()
	if base.Errors > 0 || cand.Errors > 0 {
		fmt.Fprintf(&b, "  WARNING: %d and %d searches failed in the two runs; failures count as zero\n", base.Errors, cand.Errors)
	}
	return b.String()
}
