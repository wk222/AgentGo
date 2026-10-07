package codetools

import "testing"

// Regression: an empty tool message is dropped downstream, which left the
// assistant's tool_call unanswered and made the next model request fail (HTTP 400).
func TestNonEmptyResultNeverEmpty(t *testing.T) {
	for _, in := range []string{"", "  ", "\n\t"} {
		if got := nonEmptyResult(in); got == "" {
			t.Fatalf("empty tool result leaked for %q", in)
		}
	}
	if got := nonEmptyResult("ok"); got != "ok" {
		t.Fatalf("non-empty result altered: %q", got)
	}
}
