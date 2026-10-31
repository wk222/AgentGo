// Package linktest gives tests another spelling of a folder: a link that points
// at it, or its Windows 8.3 short name. The file system treats every spelling as
// the same place, so code that judges "is this inside the workspace" or "did the
// user approve this folder" has to as well. Only tests import this package.
package linktest

import (
	"os"
	"strings"
	"testing"
)

// Dir makes link point at the directory target. It tries a symlink first and, on
// Windows where that needs a privilege, a junction, which does not. The test is
// skipped when the platform allows neither.
func Dir(t testing.TB, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if out, err := junction(link, target); err != nil {
		t.Skipf("cannot create a directory link here: %v: %s", err, out)
	}
}

// Short returns the 8.3 short spelling of an existing path (C:\Users\RUNNER~1\...).
// The test is skipped where there is no such thing: not Windows, or a volume with
// short-name creation switched off.
func Short(t testing.TB, path string) string {
	t.Helper()
	short, err := shortName(path)
	if err != nil {
		t.Skipf("no 8.3 short name for %s: %v", path, err)
	}
	if strings.EqualFold(short, path) {
		t.Skipf("the volume of %s does not create 8.3 short names", path)
	}
	return short
}
