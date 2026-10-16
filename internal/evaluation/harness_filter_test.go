package evaluation

import "testing"

func TestForHarnessSkipsInapplicableCases(t *testing.T) {
	set := EvalSet{ID: "s", Cases: []EvalCase{
		{ID: "read"},
		{ID: "write", SkipHarnesses: []string{"minimal"}},
		{ID: "other", SkipHarnesses: []string{"creative", "minimal"}},
	}}
	if got := set.ForHarness(""); len(got.Cases) != 3 {
		t.Fatalf("default harness keeps all cases, got %d", len(got.Cases))
	}
	if got := set.ForHarness("standard"); len(got.Cases) != 3 {
		t.Fatalf("standard keeps all cases, got %d", len(got.Cases))
	}
	min := set.ForHarness("minimal")
	if len(min.Cases) != 1 || min.Cases[0].ID != "read" || min.ID != "s" {
		t.Fatalf("minimal: %+v", min)
	}
	if got := set.ForHarness("creative"); len(got.Cases) != 2 {
		t.Fatalf("creative: %+v", got.Cases)
	}
}

func TestDefaultSuiteSkipsApprovalCasesOnMinimal(t *testing.T) {
	set, err := LoadSuite("suite_default.json")
	if err != nil {
		t.Fatal(err)
	}
	min := set.ForHarness("minimal")
	for _, c := range min.Cases {
		if c.ExpectApproval {
			t.Errorf("approval case %q must not run under the read-only minimal harness", c.ID)
		}
	}
	if len(min.Cases) >= len(set.Cases) {
		t.Fatal("minimal should drop some cases")
	}
}
