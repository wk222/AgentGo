package evaluation

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// EvalCase represents a single query test scenario.
type EvalCase struct {
	ID           string   `json:"id"`
	Input        string   `json:"input"`
	ExpectedText string   `json:"expected_text,omitempty"` // Regex expression or substring
	Keywords     []string `json:"keywords,omitempty"`      // Must-contain keyword tags
}

// EvalSet groups together a series of cases.
type EvalSet struct {
	ID    string     `json:"id"`
	Cases []EvalCase `json:"cases"`
}

// CaseResult holds metrics of a single execution of a case.
type CaseResult struct {
	CaseID   string        `json:"case_id"`
	RunID    int           `json:"run_id"`
	Output   string        `json:"output"`
	Score    float64       `json:"score"`
	Passed   bool          `json:"passed"`
	Duration time.Duration `json:"duration"`
	ErrorMsg string        `json:"error_msg,omitempty"`
}

// CaseSummary aggregates multiple runs of one specific case.
type CaseSummary struct {
	CaseID       string       `json:"case_id"`
	AverageScore float64      `json:"average_score"`
	PassedRuns   int          `json:"passed_runs"`
	TotalRuns    int          `json:"total_runs"`
	Passed       bool         `json:"passed"`
	Details      []CaseResult `json:"details"`
}

// EvalResult aggregates case summaries for a whole evaluation suite.
type EvalResult struct {
	EvalSetID     string        `json:"eval_set_id"`
	OverallPassed bool          `json:"overall_passed"`
	PassingRate   float64       `json:"passing_rate"`
	AverageScore  float64       `json:"average_score"`
	Duration      time.Duration `json:"duration"`
	Cases         []CaseSummary `json:"cases"`
}

// AgentGenerator abstracts the LLM Agent generation response.
type AgentGenerator interface {
	Generate(ctx context.Context, sessionID, input string) (string, error)
}

// LLMJudge defines semantic grading using an LLM model context.
type LLMJudge interface {
	Judge(ctx context.Context, input, output, expected string) (float64, string, error)
}

// ScoreCase calculates metrics based on string matching and keywords.
func ScoreCase(output string, kase EvalCase) (float64, bool) {
	if kase.ExpectedText != "" {
		re, err := regexp.Compile(kase.ExpectedText)
		if err == nil {
			if re.MatchString(output) {
				return 1.0, true
			}
		}
		if strings.Contains(output, kase.ExpectedText) {
			return 1.0, true
		}
	}

	if len(kase.Keywords) > 0 {
		matched := 0
		for _, kw := range kase.Keywords {
			if strings.Contains(strings.ToLower(output), strings.ToLower(kw)) {
				matched++
			}
		}
		score := float64(matched) / float64(len(kase.Keywords))
		// Passing condition: 60% keyword match
		return score, score >= 0.6
	}

	return 0.0, false
}

// EvaluateSet runs inference and grading on all cases.
func EvaluateSet(ctx context.Context, agent AgentGenerator, set EvalSet, numRuns int, parallel bool) (*EvalResult, error) {
	start := time.Now()
	caseSummaries := make([]CaseSummary, len(set.Cases))

	var eg errgroup.Group
	if !parallel {
		// Limit concurrency to 1 to run sequentially
		eg.SetLimit(1)
	} else {
		// Reasonable concurrency limit for API calls
		eg.SetLimit(5)
	}

	var mu sync.Mutex

	for i, c := range set.Cases {
		i, c := i, c
		eg.Go(func() error {
			var results []CaseResult
			var totalScore float64
			passedCount := 0

			for runID := 1; runID <= numRuns; runID++ {
				sessionID := fmt.Sprintf("eval-%s-run-%d", c.ID, runID)
				runStart := time.Now()

				output, err := agent.Generate(ctx, sessionID, c.Input)
				duration := time.Since(runStart)

				var score float64
				var passed bool
				var errMsg string

				if err != nil {
					errMsg = err.Error()
					score = 0.0
					passed = false
				} else {
					score, passed = ScoreCase(output, c)
				}

				if passed {
					passedCount++
				}
				totalScore += score

				results = append(results, CaseResult{
					CaseID:   c.ID,
					RunID:    runID,
					Output:   output,
					Score:    score,
					Passed:   passed,
					Duration: duration,
					ErrorMsg: errMsg,
				})
			}

			avgScore := 0.0
			if numRuns > 0 {
				avgScore = totalScore / float64(numRuns)
			}

			summary := CaseSummary{
				CaseID:       c.ID,
				AverageScore: avgScore,
				PassedRuns:   passedCount,
				TotalRuns:    numRuns,
				Passed:       passedCount == numRuns, // Passed if all runs pass
				Details:      results,
			}

			mu.Lock()
			caseSummaries[i] = summary
			mu.Unlock()
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	totalCases := len(caseSummaries)
	passedCases := 0
	var sumScore float64

	for _, s := range caseSummaries {
		if s.Passed {
			passedCases++
		}
		sumScore += s.AverageScore
	}

	passingRate := 0.0
	avgScore := 0.0
	if totalCases > 0 {
		passingRate = float64(passedCases) / float64(totalCases)
		avgScore = sumScore / float64(totalCases)
	}

	return &EvalResult{
		EvalSetID:     set.ID,
		OverallPassed: passedCases == totalCases,
		PassingRate:   passingRate,
		AverageScore:  avgScore,
		Duration:      time.Since(start),
		Cases:         caseSummaries,
	}, nil
}
