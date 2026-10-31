package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultPolicyShape(t *testing.T) {
	cwd := t.TempDir()
	extra := filepath.Join(cwd, "..", "toolchain")
	p := DefaultPolicy(cwd, []string{extra, ""}, false)

	abs := func(s string) string { a, _ := filepath.Abs(s); return a }
	if !containsRoot(p.ReadRoots, abs(cwd)) || !containsRoot(p.WriteRoots, abs(cwd)) {
		t.Fatalf("cwd must be readable and writable: %+v", p)
	}
	if !containsRoot(p.ReadRoots, abs(extra)) {
		t.Fatalf("extra read root missing: %+v", p.ReadRoots)
	}
	if containsRoot(p.WriteRoots, abs(extra)) {
		t.Fatalf("extra read root must not be writable: %+v", p.WriteRoots)
	}
	if p.AllowNetwork {
		t.Fatal("network must default to denied")
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, r := range p.ReadRoots {
			if r == home {
				t.Fatalf("HOME itself must not be a read root (secrets): %s", r)
			}
		}
	}
	seen := map[string]bool{}
	for _, r := range p.ReadRoots {
		if seen[r] {
			t.Fatalf("duplicate read root %s", r)
		}
		seen[r] = true
	}
}

func TestSplitRoots(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := SplitRoots(" a " + sep + sep + "b" + sep)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %q", got)
	}
	if SplitRoots("") != nil {
		t.Fatal("empty list should yield nil")
	}
}

func TestConfineFailsClosedWhereUnsupported(t *testing.T) {
	if Available() {
		t.Skip("sandbox available on this host")
	}
	_, err := Confine(context.Background(), "echo hi", t.TempDir(), nil, false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v (GOOS=%s)", err, runtime.GOOS)
	}
}
