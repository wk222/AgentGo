package workflow

import (
	"context"
	"fmt"
	"strings"
)

// TestResult is the outcome of one contract test.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AllPassed reports whether every result passed (and there is at least one).
func AllPassed(rs []TestResult) bool {
	for _, r := range rs {
		if !r.Passed {
			return false
		}
	}
	return len(rs) > 0
}

// RunContractTests runs every test of m for real: bind the inputs, run the
// backend, check the result against the declared outputs and then against the
// test's expectations. Each test gets its own variables.
func RunContractTests(ctx context.Context, m NodeManifest, rc RunContext) []TestResult {
	results := make([]TestResult, 0, len(m.Tests))
	for i, t := range m.Tests {
		name := t.Name
		if name == "" {
			name = fmt.Sprintf("test_%d", i+1)
		}
		results = append(results, runContractTest(ctx, m, rc, name, t))
	}
	return results
}

func runContractTest(ctx context.Context, m NodeManifest, rc RunContext, name string, t NodeTest) TestResult {
	r := TestResult{Name: name}
	rc.Vars = map[string]string{}
	inputs, err := bindInputs(m, t.Inputs, "", false)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	res, err := runNode(ctx, m, inputs, rc, nodeEnv{Vars: rc.Vars})

	if want := strings.TrimSpace(t.Expect.ErrorContains); want != "" {
		switch {
		case err == nil:
			r.Output = truncateText(res.Text, 400)
			r.Error = fmt.Sprintf("expected an error containing %q but the node succeeded", want)
		case !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)):
			r.Error = fmt.Sprintf("expected an error containing %q, got: %s", want, err)
		default:
			r.Passed = true
			r.Output = truncateText(err.Error(), 400)
		}
		return r
	}
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Output = truncateText(res.Text, 400)
	if want := t.Expect.Contains; want != "" && !strings.Contains(strings.ToLower(res.Text), strings.ToLower(want)) {
		r.Error = fmt.Sprintf("output does not contain %q", want)
		return r
	}
	for k, want := range t.Expect.Outputs {
		got, ok := res.Outputs[k]
		if !ok {
			r.Error = fmt.Sprintf("expected output %q is missing", k)
			return r
		}
		if stringifyValue(got) != stringifyValue(want) {
			r.Error = fmt.Sprintf("output %q = %s, expected %s", k, stringifyValue(got), stringifyValue(want))
			return r
		}
	}
	r.Passed = true
	return r
}
