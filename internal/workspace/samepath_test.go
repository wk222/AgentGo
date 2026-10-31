package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentgo/internal/linktest"
)

func TestSamePathSeesThroughLinksAndShortNames(t *testing.T) {
	real := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	linkDirForTest(t, link, real)

	if !SamePath(real, link) || !SamePath(link, real) {
		t.Errorf("a link and its target are the same folder: %q %q", real, link)
	}
	if SamePath(real, other) {
		t.Error("two different folders are not the same")
	}
	if SamePath(real, filepath.Dir(real)) || SamePath(filepath.Join(real, "sub"), real) {
		t.Error("a folder is not its parent or its child")
	}
	if !SamePath(real, real+string(filepath.Separator)) {
		t.Error("a trailing separator does not make another folder")
	}
}

func TestSamePathWithAWindowsShortName(t *testing.T) {
	long := filepath.Join(t.TempDir(), "a-rather-long-folder-name")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	short := linktest.Short(t, long) // skips where there is no 8.3 name
	if !SamePath(long, short) || !SamePath(short, long) {
		t.Errorf("the short and the long spelling are the same folder: %q %q", short, long)
	}
}

func TestSamePathCaseRuleFollowsThePlatform(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "CaseFolder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(filepath.Dir(dir), strings.ToUpper(filepath.Base(dir)))
	switch runtime.GOOS {
	case "windows":
		if !SamePath(dir, upper) {
			t.Error("Windows ignores case in file names")
		}
	case "linux":
		if SamePath(dir, upper) {
			t.Error("Linux file names are case sensitive")
		}
	default:
		t.Skip("case rules differ per volume on " + runtime.GOOS)
	}
	// A path that does not exist is compared by spelling, with the same rule.
	missing := filepath.Join(dir, "missing")
	if !SamePath(missing, missing) {
		t.Error("the same missing path is the same")
	}
}
