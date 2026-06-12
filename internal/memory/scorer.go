package memory

import (
	"math"
	"strings"
)

// Feedback deltas aligned with PyBot memory/scoring.py
var feedbackDeltas = map[FeedbackKind]float64{
	FeedbackPositive:  0.15,
	FeedbackNegative:  -0.10,
	FeedbackDisproved: -0.50,
}

func feedbackDelta(kind FeedbackKind) float64 {
	if d, ok := feedbackDeltas[kind]; ok {
		return d
	}
	return 0
}

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func bm25LiteScore(queryTokens []string, content string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	doc := strings.ToLower(content)
	var score float64
	for _, t := range queryTokens {
		if strings.Contains(doc, t) {
			score += 1
		}
	}
	return score / float64(len(queryTokens))
}

func recencyDecay(lastRecall int64, now int64, alpha float64) float64 {
	if lastRecall <= 0 {
		return 1.0
	}
	days := float64(now-lastRecall) / 86400.0
	return math.Max(0.3, math.Exp(-alpha*days))
}

func forgettingCurveDecay(createdAt int64, now int64, lambda float64) float64 {
	if createdAt <= 0 {
		return 1.0
	}
	days := float64(now-createdAt) / 86400.0
	return math.Max(0.1, math.Exp(-lambda*days))
}

func combinedScore(relevance, importance, decay float64) float64 {
	return relevance * importance * decay
}
