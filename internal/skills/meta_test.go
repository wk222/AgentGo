package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontMatterNamesTheSkillAndStaysOutOfTheContent(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "skills", "folder-name")
	_ = os.MkdirAll(filepath.Join(dir, "scripts"), 0o755)
	doc := "---\r\nname: weekly-report\r\ndescription: 用导出的 CSV 生成周报。\r\nrequires:\r\n  bins: [python]\r\ncredentials: [SALES_TOKEN]\r\n---\r\n# 周报\r\n\r\n正文。\r\n"
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "scripts", "build.py"), []byte("print(1)"), 0o644)

	got := byID(NewLoader(ws).Reload())
	s, ok := got["skill:weekly-report"]
	if !ok {
		t.Fatalf("front matter name must win over the folder name: %v", got)
	}
	if s.Description != "用导出的 CSV 生成周报。" || strings.Contains(s.Content, "credentials") || !strings.HasPrefix(s.Content, "# 周报") {
		t.Fatalf("skill = %+v", s)
	}
	if s.Dir != dir || len(s.Requires.Bins) != 1 || len(s.Credentials) != 1 {
		t.Fatalf("skill = %+v", s)
	}
	if res := Resources(s); len(res) != 1 || res[0] != "scripts/build.py" {
		t.Fatalf("resources = %v", res)
	}
}

func TestStrayDashesAreNotFrontMatter(t *testing.T) {
	for _, doc := range []string{"# T\n\n---\nnot front matter\n---\n", "---\nunclosed\n# T\n", "----\na: b\n----\n", "---\n: : bad yaml [\n---\nbody\n"} {
		m, body := SplitFrontMatter(doc)
		if body != doc || m.Name != "" {
			t.Errorf("%q: meta %+v body %q", doc, m, body)
		}
	}
}

func TestCheckReportsNamesOnlyAndCredentialOrder(t *testing.T) {
	l := NewLoader(t.TempDir())
	credFile := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(credFile, []byte(`{"FROM_FILE":"file-secret","BOTH":"file-value"}`), 0o600)
	l.SetCredentialFile(credFile)
	t.Setenv("BOTH", "env-value")

	if v, _ := l.Credential("BOTH"); v != "env-value" {
		t.Fatalf("the environment wins: %q", v)
	}
	if v, ok := l.Credential("FROM_FILE"); !ok || v != "file-secret" {
		t.Fatalf("file credential = %q %v", v, ok)
	}
	s := Skill{Requires: Requires{Bins: []string{"definitely-not-installed-xyz"}}, Credentials: []string{"FROM_FILE", "NOPE_NOT_SET"}}
	r := l.Check(s)
	if r.OK || len(r.MissingBins) != 1 || len(r.MissingCredentials) != 1 || r.MissingCredentials[0] != "NOPE_NOT_SET" {
		t.Fatalf("readiness = %+v", r)
	}
	if !l.Check(Skill{}).OK {
		t.Fatal("a skill with no requirements is ready")
	}
}
