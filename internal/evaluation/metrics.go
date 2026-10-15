package evaluation

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Metrics is what one agent run consumed.
type Metrics struct {
	ModelCalls       int `json:"model_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
	ToolCalls        int `json:"tool_calls"`
}

// Add accumulates o into m.
func (m *Metrics) Add(o Metrics) {
	m.ModelCalls += o.ModelCalls
	m.PromptTokens += o.PromptTokens
	m.CachedTokens += o.CachedTokens
	m.CompletionTokens += o.CompletionTokens
	m.ReasoningTokens += o.ReasoningTokens
	m.ToolCalls += o.ToolCalls
}

// CacheHitRate is cached / prompt tokens; 0 when no prompt was sent.
func (m Metrics) CacheHitRate() float64 {
	if m.PromptTokens <= 0 {
		return 0
	}
	return float64(m.CachedTokens) / float64(m.PromptTokens)
}

// MeasuredAgent is an AgentGenerator that also reports what the run consumed.
// EvaluateSet uses it automatically when the agent implements it.
type MeasuredAgent interface {
	AgentGenerator
	GenerateMeasured(ctx context.Context, sessionID, input string) (string, Metrics, error)
}

//go:embed suite_default.json
var defaultSuiteJSON []byte

// DefaultSuite returns the built-in regression suite.
func DefaultSuite() (EvalSet, error) {
	var set EvalSet
	err := json.Unmarshal(defaultSuiteJSON, &set)
	return set, err
}

// LoadSuite reads an EvalSet from a JSON file.
func LoadSuite(path string) (EvalSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return EvalSet{}, err
	}
	var set EvalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return EvalSet{}, fmt.Errorf("parse suite %s: %w", path, err)
	}
	return set, nil
}

// SaveResult writes a result as indented JSON.
func SaveResult(path string, r *EvalResult) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadResult reads a result written by SaveResult.
func LoadResult(path string) (*EvalResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r EvalResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse result %s: %w", path, err)
	}
	return &r, nil
}

// Thresholds define what counts as a regression against a baseline.
type Thresholds struct {
	MaxPassDrop     float64 // absolute drop of the pass rate, e.g. 0.10 = 10 points
	MaxTokenGrowth  float64 // relative growth of prompt tokens per run, e.g. 0.20 = +20%
	MaxCacheHitDrop float64 // absolute drop of the cache hit rate (warning only)
}

// DefaultThresholds are the limits documented in docs/eval-baseline.md.
func DefaultThresholds() Thresholds {
	return Thresholds{MaxPassDrop: 0.10, MaxTokenGrowth: 0.20, MaxCacheHitDrop: 0.10}
}

// Finding is one comparison outcome. Fail findings must make CI/exit codes fail.
type Finding struct {
	Fail bool   `json:"fail"`
	Msg  string `json:"msg"`
}

// Compare checks cur against base. Per-run averages are compared so the runs
// count does not have to match. Cases that passed in the baseline and fail now
// are always reported, but only the aggregate numbers decide failure: a single
// flaky model answer must not break the build.
func Compare(base, cur *EvalResult, th Thresholds) []Finding {
	var out []Finding
	if base == nil || cur == nil {
		return []Finding{{Fail: true, Msg: "baseline or current result is missing"}}
	}

	if drop := base.PassingRate - cur.PassingRate; drop > th.MaxPassDrop+1e-9 {
		out = append(out, Finding{Fail: true, Msg: fmt.Sprintf(
			"pass rate %.0f%% -> %.0f%% (dropped %.0f points, limit %.0f)",
			base.PassingRate*100, cur.PassingRate*100, drop*100, th.MaxPassDrop*100)})
	}

	baseCases := make(map[string]bool, len(base.Cases))
	for _, c := range base.Cases {
		baseCases[c.CaseID] = c.Passed
	}
	var regressed []string
	for _, c := range cur.Cases {
		if baseCases[c.CaseID] && !c.Passed {
			regressed = append(regressed, c.CaseID)
		}
	}
	sort.Strings(regressed)
	if len(regressed) > 0 {
		out = append(out, Finding{Msg: "cases that passed in the baseline but fail now: " + strings.Join(regressed, ", ")})
	}

	if bp, cp := perRun(base.Totals.PromptTokens, base.TotalRuns), perRun(cur.Totals.PromptTokens, cur.TotalRuns); bp > 0 {
		if g := (cp - bp) / bp; g > th.MaxTokenGrowth+1e-9 {
			out = append(out, Finding{Fail: true, Msg: fmt.Sprintf(
				"prompt tokens per run %.0f -> %.0f (+%.0f%%, limit +%.0f%%)", bp, cp, g*100, th.MaxTokenGrowth*100)})
		}
	}

	if base.Totals.PromptTokens > 0 && cur.Totals.PromptTokens > 0 {
		bh, ch := base.Totals.CacheHitRate(), cur.Totals.CacheHitRate()
		if bh-ch > th.MaxCacheHitDrop+1e-9 {
			out = append(out, Finding{Msg: fmt.Sprintf(
				"cache hit rate %.0f%% -> %.0f%% (model-side noise is common; check before blaming the prefix)", bh*100, ch*100)})
		}
	}
	return out
}

// HasFailure reports whether any finding is a hard failure.
func HasFailure(fs []Finding) bool {
	for _, f := range fs {
		if f.Fail {
			return true
		}
	}
	return false
}

func perRun(total, runs int) float64 {
	if runs <= 0 {
		return 0
	}
	return float64(total) / float64(runs)
}
