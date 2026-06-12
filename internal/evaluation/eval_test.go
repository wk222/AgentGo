package evaluation

import (
	"context"
	"strings"
	"testing"
)

// MockAgent implements the AgentGenerator interface.
type MockAgent struct{}

func (m *MockAgent) Generate(ctx context.Context, sessionID, input string) (string, error) {
	if strings.Contains(input, "France") {
		return "The capital of France is Paris.", nil
	}
	if strings.Contains(input, "code") {
		return "Here is your generated code fragment.", nil
	}
	return "unknown input", nil
}

func TestEvaluateSet(t *testing.T) {
	agent := &MockAgent{}

	set := EvalSet{
		ID: "demo-eval-set",
		Cases: []EvalCase{
			{
				ID:           "case-capital",
				Input:        "What is the capital of France?",
				Keywords:     []string{"Paris", "capital"},
				ExpectedText: "Paris",
			},
			{
				ID:           "case-code",
				Input:        "Please generate python code",
				ExpectedText: `.*code.*`,
			},
		},
	}

	// Run evaluation with 2 runs per case, in parallel
	result, err := EvaluateSet(context.Background(), agent, set, 2, true)
	if err != nil {
		t.Fatalf("failed evaluating set: %v", err)
	}

	if result.EvalSetID != "demo-eval-set" {
		t.Errorf("expected eval set ID demo-eval-set, got %s", result.EvalSetID)
	}

	if !result.OverallPassed {
		t.Errorf("expected overall passed to be true")
	}

	if result.PassingRate != 1.0 {
		t.Errorf("expected passing rate to be 1.0, got %f", result.PassingRate)
	}

	if result.AverageScore < 0.9 {
		t.Errorf("expected average score close to 1.0, got %f", result.AverageScore)
	}

	// Verify Case 1 details
	case1 := result.Cases[0]
	if case1.CaseID != "case-capital" {
		t.Errorf("expected first case to be case-capital, got %s", case1.CaseID)
	}
	if case1.PassedRuns != 2 {
		t.Errorf("expected 2 passed runs, got %d", case1.PassedRuns)
	}
}
