package evolution

import (
	"strings"
	"testing"
)

func TestGuardSkipsShortStrings(t *testing.T) {
	g := NewGuard()
	g.Forbid("held-out expectation", "ok", "file", "The Answer Is 42 Exactly")
	if g.Skipped() != 2 || g.Len() != 1 {
		t.Fatalf("skipped=%d enforced=%d", g.Skipped(), g.Len())
	}
	if err := g.ScanText("a.md", "ok, a file"); err != nil {
		t.Fatalf("short strings must not be enforced: %v", err)
	}
	// A different case and a line wrap must not hide the match.
	err := g.ScanText("skills/x/SKILL.md", "note: the answer is\n   42   EXACTLY, remember")
	if err == nil || !strings.Contains(err.Error(), "held-out expectation") || !strings.Contains(err.Error(), "skills/x/SKILL.md") {
		t.Fatalf("leak not reported: %v", err)
	}
}

func TestGuardVerdictMarkers(t *testing.T) {
	g := NewGuard()
	g.ForbidVerdictMarkers()
	err := g.ScanTree(Tree{"skills/x/SKILL.md": "if it says Compromised: wrote file, retry"})
	if err == nil || !strings.Contains(err.Error(), "eval verdict text") || !strings.Contains(err.Error(), "skills/x/SKILL.md") {
		t.Fatalf("verdict text not caught: %v", err)
	}
	if err := g.ScanTree(Tree{"AGENTS.md": "Prefer small diffs."}); err != nil {
		t.Fatalf("clean tree rejected: %v", err)
	}
}

func TestScanTreeChecksFileNames(t *testing.T) {
	g := NewGuard()
	g.Forbid("held-out case id", "inj-readme-secret-01")
	err := g.ScanTree(Tree{"skills/inj-readme-secret-01/SKILL.md": "harmless"})
	if err == nil || !strings.Contains(err.Error(), "file name") {
		t.Fatalf("case id in a file name not caught: %v", err)
	}
}

func TestScanDir(t *testing.T) {
	root := t.TempDir()
	g := NewGuard()
	g.ForbidVerdictMarkers()
	if err := NewGuard().ScanDir(root); err != nil {
		t.Fatalf("an empty guard must pass: %v", err)
	}

	write(t, root, "context/rollouts/a.md", "the agent listed the directory")
	write(t, root, "blob.bin", "Compromised: hidden in a binary") // not a text file: not interpreted
	if err := g.ScanDir(root); err != nil {
		t.Fatalf("benign evidence rejected: %v", err)
	}

	write(t, root, "context/deliverables/x/INDEX.md", "judge says: did not match expectations")
	err := g.ScanDir(root)
	if err == nil || !strings.Contains(err.Error(), "context/deliverables/x/INDEX.md") {
		t.Fatalf("leak in a nested file not caught: %v", err)
	}
}
