package pathgrant

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGrantCoversTheFolderAndWhatIsBelowIt(t *testing.T) {
	r := New()
	root := t.TempDir()
	shared := t.TempDir()
	other := t.TempDir()

	if r.Allowed(root, filepath.Join(shared, "a.xlsx")) {
		t.Fatal("nothing is granted yet")
	}
	if !r.Grant(root, shared) {
		t.Fatal("a plain folder can be granted")
	}
	for _, p := range []string{shared, filepath.Join(shared, "a.xlsx"), filepath.Join(shared, "sub", "deep", "b.txt")} {
		if !r.Allowed(root, p) {
			t.Errorf("%s should be inside the grant", p)
		}
	}
	if r.Allowed(root, filepath.Join(other, "a.xlsx")) {
		t.Error("a sibling folder is not granted")
	}
	if r.Allowed(root, shared+"x") || r.Allowed(root, filepath.Dir(shared)) {
		t.Error("a name prefix or the parent folder is not granted")
	}
	if r.Allowed(t.TempDir(), filepath.Join(shared, "a.xlsx")) {
		t.Error("a grant belongs to one workspace")
	}
	if got := r.List(root); len(got) != 1 {
		t.Errorf("List = %v", got)
	}
	r.Revoke(root)
	if r.Allowed(root, filepath.Join(shared, "a.xlsx")) {
		t.Error("revoked")
	}
}

func TestGrantRefusesPlacesThatMustStayClosed(t *testing.T) {
	r := New()
	root := t.TempDir()
	data := t.TempDir()
	r.Protect(data)

	vol := filepath.VolumeName(root)
	driveRoot := vol + string(filepath.Separator)
	cases := map[string]string{driveRoot: "drive root", filepath.Join(data, "config.json"): "app data"}
	if runtime.GOOS == "windows" {
		if win := os.Getenv("SystemRoot"); win != "" {
			cases[filepath.Join(win, "System32", "drivers", "etc", "hosts")] = "system folder"
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		cases[filepath.Join(home, ".ssh", "id_rsa")] = "credentials"
	}
	for p, what := range cases {
		if r.Forbidden(p) == "" {
			t.Errorf("%s (%s) must be forbidden", p, what)
		}
		if r.Grant(root, p) || r.Allowed(root, p) {
			t.Errorf("%s (%s) must not be grantable", p, what)
		}
	}
}

func TestDirForGrantsTheFolderNotTheDrive(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.xlsx")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DirFor(file, false); got != dir {
		t.Errorf("a file grants its folder: %q, want %q", got, dir)
	}
	if got := DirFor(dir, false); got != dir {
		t.Errorf("an existing folder grants itself: %q", got)
	}
	if got := DirFor(filepath.Join(dir, "new", "out.xlsx"), true); got != filepath.Join(dir, "new", "out.xlsx") {
		t.Errorf("a path under a directory key is the folder: %q", got)
	}
	rootFile := filepath.VolumeName(dir) + string(filepath.Separator) + "top.xlsx"
	if got := DirFor(rootFile, false); got != rootFile {
		t.Errorf("a file in a drive root is granted alone: %q", got)
	}
}
