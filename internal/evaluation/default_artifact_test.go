package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func defaultArtifactCase(t *testing.T, id string) EvalCase {
	t.Helper()
	suite, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	if err = suite.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range suite.Cases {
		if c.ID == id {
			c.root = t.TempDir()
			return c
		}
	}
	t.Fatal("case missing", id)
	return EvalCase{}
}

func TestDefaultArtifactsRejectClaimsAndAcceptActualProducts(t *testing.T) {
	for _, id := range []string{"task-fix-total-tests", "task-csv-report-verified", "task-approved-file-content"} {
		t.Run(id, func(t *testing.T) {
			c := defaultArtifactCase(t, id)
			if result := runCase(context.Background(), &probeAgent{fn: func() {}}, "claim", c); result.Passed {
				t.Fatal("claim without an artifact passed")
			}
			writer := func() {
				var file, body string
				switch id {
				case "task-fix-total-tests":
					file = "calc.go"
					body = "package evalartifact\nfunc Total(values []int) int { sum:=0;for _,value:=range values{sum+=value};return sum }\n"
				case "task-csv-report-verified":
					file = "report.csv"
					body = "区县,目标,完成,差距\n城东,150,120,30\n城西,120,90,30\n"
				case "task-approved-file-content":
					file = "note.txt"
					body = "hello-eval-7421\n"
				}
				path := filepath.Join(c.root, filepath.FromSlash(c.Scratch), file)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if result := runCase(context.Background(), &probeAgent{fn: writer}, "actual", c); !result.Passed {
				t.Fatal("valid artifact rejected", result.ErrorMsg)
			}
		})
	}
}

func TestArtifactVerificationRejectsTamperedTestsAndWrongCSV(t *testing.T) {
	c := defaultArtifactCase(t, "task-fix-total-tests")
	result := runCase(context.Background(), &probeAgent{fn: func() {
		if err := os.WriteFile(filepath.Join(c.root, c.Scratch, "calc_test.go"), []byte("package evalartifact\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}}, "tamper", c)
	if result.Passed || !strings.Contains(result.ErrorMsg, "protected fixture") {
		t.Fatal("test tampering passed", result)
	}
	c = defaultArtifactCase(t, "task-csv-report-verified")
	result = runCase(context.Background(), &probeAgent{fn: func() {
		if err := os.WriteFile(filepath.Join(c.root, c.Scratch, "report.csv"), []byte("区县,目标,完成,差距\n城东,150,120,30\n城西,999,90,30\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}}, "wrong-data", c)
	if result.Passed || !strings.Contains(result.ErrorMsg, "failed") {
		t.Fatal("wrong CSV passed marker matching", result)
	}
}

func TestDefaultHarnessQuestionMatchesCurrentFrontend(t *testing.T) {
	suite, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	if suite.ID != "agentgo-default-v3" {
		t.Fatal("suite version not updated")
	}
	for _, c := range suite.Cases {
		if c.ID == "fact-harness-ids" {
			if !strings.Contains(c.Input, "frontend/src/shared/harness.ts") {
				t.Fatal("stale harness source path")
			}
			raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "shared", "harness.ts"))
			if err != nil {
				t.Fatal(err)
			}
			var actual []string
			for _, match := range regexp.MustCompile(`\{\s*id:\s*'([^']+)'`).FindAllStringSubmatch(string(raw), -1) {
				actual = append(actual, match[1])
			}
			expected := append([]string(nil), c.Keywords...)
			sort.Strings(actual)
			sort.Strings(expected)
			if len(actual) == 0 || strings.Join(actual, ",") != strings.Join(expected, ",") {
				t.Fatalf("harness expectations drifted: actual=%v expected=%v", actual, expected)
			}
			return
		}
	}
	t.Fatal("harness case missing")
}

func TestDefaultQuestionSourcePathsExist(t *testing.T) {
	suite, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	paths := regexp.MustCompile(`(?:frontend|internal|cmd)/[a-zA-Z0-9_./-]+\.(?:go|tsx?|json|log)`)
	for _, c := range suite.Cases {
		for _, path := range paths.FindAllString(c.Input, -1) {
			if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(path))); err != nil {
				t.Errorf("case %s points at missing source %s: %v", c.ID, path, err)
			}
		}
	}
}

func TestCompareRefusesIncompatibleBaselines(t *testing.T) {
	base := &EvalResult{EvalSetID: "v2", Harness: "standard", Cases: []CaseSummary{{CaseID: "one"}}}
	for _, current := range []*EvalResult{
		{EvalSetID: "v3", Harness: "standard", Cases: base.Cases},
		{EvalSetID: "v2", Harness: "ultimate", Cases: base.Cases},
		{EvalSetID: "v2", Harness: "standard", Cases: []CaseSummary{{CaseID: "different"}}},
	} {
		if !HasFailure(Compare(base, current, DefaultThresholds())) {
			t.Fatal("incomparable baseline accepted")
		}
	}
}

func TestFixtureChecksRejectInvalidDeclarationsAndScratchLinks(t *testing.T) {
	for _, check := range []EvalCheck{{Path: "missing", Unchanged: true}, {Path: "a", Unchanged: true, Absent: true}} {
		if err := (EvalSet{Cases: []EvalCase{{ID: "bad", Scratch: "s", Setup: []EvalFile{{Path: "a", Content: "x"}}, Verify: []EvalCheck{check}}}}).Validate(); err == nil {
			t.Fatal("invalid unchanged check accepted")
		}
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if _, err := inside(root, "linked/child"); err == nil {
		t.Fatal("scratch traversed a symlink")
	}
	if _, err := inside(root, "linked"); err == nil {
		t.Fatal("scratch symlink accepted")
	}
}
