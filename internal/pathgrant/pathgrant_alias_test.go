package pathgrant

import (
	"os"
	"path/filepath"
	"testing"

	"agentgo/internal/linktest"
)

// A folder has one identity and many spellings: through a link, or by its
// Windows short name. A grant made under one spelling has to hold under the
// others, and a closed folder has to stay closed whichever way it is named.

// twoSpellings makes a folder and a link to it.
func twoSpellings(t *testing.T) (real, alias string) {
	t.Helper()
	base := t.TempDir()
	real = filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias = filepath.Join(base, "alias")
	linktest.Dir(t, alias, real)
	return real, alias
}

func TestGrantHoldsWhicheverWayTheFolderIsSpelled(t *testing.T) {
	sharedReal, sharedAlias := twoSpellings(t)
	rootReal, rootAlias := twoSpellings(t)
	r := New()

	// The approval names the folder through a link, from a workspace named through a link.
	if !r.Grant(rootAlias, sharedAlias) {
		t.Fatal("the grant was refused")
	}
	for _, c := range []struct{ root, path string }{
		{rootReal, filepath.Join(sharedReal, "a.xlsx")},
		{rootAlias, filepath.Join(sharedReal, "sub", "b.txt")},
		{rootReal, filepath.Join(sharedAlias, "new", "c.xlsx")},
	} {
		if !r.Allowed(c.root, c.path) {
			t.Errorf("%s in workspace %s must be inside the grant", c.path, c.root)
		}
	}

	// The same folder named another way is not a second grant, and is listed by its real place.
	if !r.Grant(rootReal, sharedReal) {
		t.Fatal("granting the same folder again was refused")
	}
	if got := r.List(rootReal); len(got) != 1 || got[0] != Resolve(sharedReal) {
		t.Errorf("List = %v, want just %q", got, Resolve(sharedReal))
	}

	// The other direction: granted by the real path, used through the link.
	r2 := New()
	r2.Grant(rootReal, sharedReal)
	if !r2.Allowed(rootAlias, filepath.Join(sharedAlias, "a.xlsx")) {
		t.Error("a grant made by the real path must hold when the folder is named through a link")
	}
	if r2.Allowed(rootAlias, filepath.Join(t.TempDir(), "a.xlsx")) {
		t.Error("an unrelated folder is not granted")
	}
}

func TestForbiddenSeesThroughALink(t *testing.T) {
	data, alias := twoSpellings(t)
	r := New()
	r.Protect(data)

	if r.Forbidden(filepath.Join(data, "config.json")) == "" {
		t.Fatal("the protected folder itself must be forbidden")
	}
	// Reaching the folder through a link must not be a way around the list.
	if r.Forbidden(filepath.Join(alias, "config.json")) == "" {
		t.Error("a link to a protected folder must stay forbidden")
	}
	if r.Grant(t.TempDir(), alias) {
		t.Error("a link to a protected folder must not be grantable")
	}
}

func TestGrantHoldsForAWindowsShortName(t *testing.T) {
	long := filepath.Join(t.TempDir(), "a-rather-long-folder-name")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	short := linktest.Short(t, long) // skips where there is no 8.3 name
	root := t.TempDir()

	r := New()
	if !r.Grant(root, short) {
		t.Fatal("the grant was refused")
	}
	if !r.Allowed(root, filepath.Join(long, "x.txt")) {
		t.Error("a grant made by the short name must cover the long one")
	}

	r2 := New()
	r2.Grant(root, long)
	if !r2.Allowed(root, filepath.Join(short, "x.txt")) {
		t.Error("a grant made by the long name must cover the short one")
	}
}
