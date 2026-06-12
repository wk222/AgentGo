package workflow

import "testing"

func TestMatchWhenContains(t *testing.T) {
	if !matchWhen("contains:ok", "status ok") {
		t.Fatal("expected contains match")
	}
	if matchWhen("contains:missing", "status ok") {
		t.Fatal("expected no match")
	}
}
