package admin

import "testing"

func TestReplanAfterFailureAppendsRecovery(t *testing.T) {
	steps := ReplanAfterFailure("fix app", "step 2", "APP_RUNTIME_ERROR: boom")
	if len(steps) < 4 {
		t.Fatalf("expected replan steps >= 4, got %d", len(steps))
	}
	last := steps[len(steps)-1]
	if last == "" || !containsSub(last, "失败恢复") {
		t.Fatalf("missing recovery step: %q", last)
	}
}

func TestIsRuntimeHealError(t *testing.T) {
	if !IsRuntimeHealError(nil, "APP_RUNTIME_ERROR in output") {
		t.Fatal("expected runtime heal detection")
	}
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
