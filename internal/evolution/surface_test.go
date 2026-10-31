package evolution

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func TestCleanRejectsEscapes(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../x", "/etc/passwd", "C:/x", `a\b`, "a//b", "a/./b", "skills/../AGENTS.md"} {
		if _, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) was accepted", bad)
		}
	}
	for _, ok := range []string{"AGENTS.md", "skills/a/SKILL.md"} {
		if got, err := Clean(ok); err != nil || got != ok {
			t.Errorf("Clean(%q) = %q, %v", ok, got, err)
		}
	}
}

func TestEditableSurface(t *testing.T) {
	for _, rel := range []string{"AGENTS.md", "skills/foo/SKILL.md", "skills/a_b-1/SKILL.md"} {
		if !Editable(rel) {
			t.Errorf("%s should be editable", rel)
		}
	}
	for _, rel := range []string{
		"agents.md", "skills/Foo/SKILL.md", "skills/foo/skill.md", "skills/foo/scripts/x.py",
		"skills/foo/bar/SKILL.md", "docs/a.md", "internal/x.go", "config.json", "skills/SKILL.md",
		"skills/-x/SKILL.md",
	} {
		if Editable(rel) {
			t.Errorf("%s must not be editable", rel)
		}
	}
}

func TestReadTreeRejectsOffSurface(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "rules")
	write(t, root, "skills/a/SKILL.md", "desc")
	write(t, root, "skills/a/run.py", "print(1)")
	write(t, root, "notes.txt", "n")
	write(t, root, "skills/b/SKILL.md", strings.Repeat("x", 100))

	tree, rejected, err := ReadTree(root, Limits{MaxFileBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(tree); !reflect.DeepEqual(got, []string{"AGENTS.md", "skills/a/SKILL.md"}) {
		t.Fatalf("tree = %v", got)
	}
	if len(rejected) != 3 || rejected[0] != "notes.txt" || rejected[1] != "skills/a/run.py" ||
		!strings.HasPrefix(rejected[2], "skills/b/SKILL.md (100 bytes") {
		t.Fatalf("rejected = %v", rejected)
	}
}

func TestReadTreeNeverFollowsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write(t, outside, "x/SKILL.md", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	tree, rejected, err := ReadTree(root, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 0 || !reflect.DeepEqual(rejected, []string{"skills (symlink)"}) {
		t.Fatalf("tree=%v rejected=%v", tree, rejected)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	tree := Tree{
		"config.json":           "{}",
		"skills/empty/SKILL.md": "  \n",
		"skills/long/SKILL.md":  "# t\n" + strings.Repeat("x", MaxSkillBytes),
		"skills/head/SKILL.md":  "# only a heading\n",
		"skills/Bad/SKILL.md":   "x",
	}
	err := Validate(tree, DefaultLimits)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"config.json is off the editable surface",
		"skills/empty/SKILL.md is empty",
		"skills/long/SKILL.md is",
		"dead text",
		"skills/head/SKILL.md has no description",
		"skills/Bad/SKILL.md is off the editable surface",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestValidateAcceptsGoodTreeAndRejectsEmpty(t *testing.T) {
	good := Tree{
		"AGENTS.md":              "Prefer small diffs.",
		"skills/review/SKILL.md": "# Review\n\nCheck the diff before you commit.",
	}
	if err := Validate(good, DefaultLimits); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Tree{}, DefaultLimits); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty tree: %v", err)
	}
}

func TestValidateLimits(t *testing.T) {
	two := Tree{"AGENTS.md": "aaaaaaaaaa", "skills/a/SKILL.md": "bbbbbbbbbb"}
	if err := Validate(two, Limits{MaxFiles: 1}); err == nil || !strings.Contains(err.Error(), "at most 1") {
		t.Errorf("file count: %v", err)
	}
	if err := Validate(two, Limits{MaxTotalBytes: 15}); err == nil || !strings.Contains(err.Error(), "in total") {
		t.Errorf("total size: %v", err)
	}
	if err := Validate(two, Limits{MaxFileBytes: 5}); err == nil || !strings.Contains(err.Error(), "limit is 5") {
		t.Errorf("file size: %v", err)
	}
}

func TestWriteTreeAppliesDeletionsAndKeepsOthers(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "old")
	write(t, root, "skills/old/SKILL.md", "old skill")
	write(t, root, "skills/old/asset.txt", "keep")
	write(t, root, "skills/gone/SKILL.md", "bye")
	write(t, root, "other.txt", "untouched")

	want := Tree{"AGENTS.md": "new", "skills/new/SKILL.md": "fresh skill"}
	if err := WriteTree(root, want); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "AGENTS.md") != "new" || read(t, root, "skills/new/SKILL.md") != "fresh skill" {
		t.Fatal("new content not written")
	}
	if exists(root, "skills/old/SKILL.md") {
		t.Error("removed skill is still active")
	}
	if !exists(root, "skills/old/asset.txt") {
		t.Error("a file beside the removed SKILL.md was deleted")
	}
	if exists(root, "skills/gone") {
		t.Error("an emptied skill directory was left behind")
	}
	if read(t, root, "other.txt") != "untouched" {
		t.Error("an off-surface file was changed")
	}
	got, _, err := ReadTree(root, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(want, got); !d.Empty() {
		t.Fatalf("surface differs from the written tree: %+v", d)
	}
}

func TestWriteTreeRejectsOffSurface(t *testing.T) {
	root := t.TempDir()
	if err := WriteTree(root, Tree{"main.go": "package main"}); err == nil {
		t.Fatal("an off-surface path was written")
	}
	if exists(root, "main.go") {
		t.Fatal("main.go was created")
	}
}

func TestWriteTreeRefusesSymlinkedParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	if err := WriteTree(root, Tree{"skills/a/SKILL.md": "x"}); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if exists(outside, "a") {
		t.Fatal("the write escaped the workspace")
	}
}

func TestDiff(t *testing.T) {
	base := Tree{"A": "1", "B": "2", "C": "3"}
	next := Tree{"A": "1", "B": "x", "D": "4"}
	d := Diff(base, next)
	if !reflect.DeepEqual(d.Added, []string{"D"}) || !reflect.DeepEqual(d.Removed, []string{"C"}) ||
		!reflect.DeepEqual(d.Modified, []string{"B"}) || d.String() != "+1 -1 ~1" || d.Empty() {
		t.Fatalf("diff = %+v", d)
	}
	if !Diff(base, base).Empty() {
		t.Fatal("identical trees must diff empty")
	}
}
