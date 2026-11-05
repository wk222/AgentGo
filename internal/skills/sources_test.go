package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root string, rel string, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel), "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	return home
}

func TestUserFolderSitsBetweenTheWorkspaceAndTheBuiltInSkills(t *testing.T) {
	ws, data := t.TempDir(), t.TempDir()
	writeSkill(t, ws, "skills/shared", "# shared\n\nworkspace version of the skill.\n")
	writeSkill(t, data, "skills/shared", "# shared\n\nuser version of the skill.\n")
	writeSkill(t, data, "skills/mine", "# mine\n\nonly the user has this one.\n")
	writeSkill(t, data, "skills/office-pptx", "# office-pptx\n\nthe user's own deck rules.\n")

	l := NewLoader(ws)
	l.SetSources(DefaultSources(data))
	got := byID(l.Reload())

	if s := got["skill:shared"]; s.Scope != ScopeWorkspace || !strings.Contains(s.Content, "workspace version") {
		t.Errorf("the workspace must win over the user folder: %+v", s)
	}
	if s := got["skill:mine"]; s.Scope != ScopeUser || s.Dir == "" {
		t.Errorf("user skill: %+v", s)
	}
	if s := got["skill:office-pptx"]; s.Scope != ScopeUser || !strings.Contains(s.Content, "deck rules") {
		t.Errorf("the user folder must win over a built-in skill: %+v", s)
	}
	if got["skill:office-xlsx"].Scope != ScopeBuiltin {
		t.Error("the other built-in skills must stay")
	}
}

func TestOtherAgentsSkillsAreOffUnlessAsked(t *testing.T) {
	home := fakeHome(t)
	writeSkill(t, home, ".claude/skills/pdf-fill", "---\nname: pdf-fill\ndescription: Fill in PDF forms.\n---\n# pdf-fill\n")
	writeSkill(t, home, ".claude/skills/same", "# same\n\nthe claude copy.\n")
	writeSkill(t, home, ".agents/skills/same", "# same\n\nthe agents copy.\n")
	writeSkill(t, home, ".codex/skills/.system/hidden", "# hidden\n\nCodex's own.\n")
	writeSkill(t, home, ".claude/skills/node_modules/pkg/x", "# x\n\nnot a skill.\n")
	writeSkill(t, home, ".claude/skills/a/b/c/too-deep", "# too-deep\n\nthree levels down.\n")
	writeSkill(t, home, ".claude/skills/group/nested", "# nested\n\ntwo levels down is fine.\n")
	ws, data := t.TempDir(), t.TempDir()

	load := func() map[string]Skill {
		l := NewLoader(ws)
		l.SetSources(DefaultSources(data))
		return byID(l.Reload())
	}

	got := load()
	for _, id := range []string{"skill:pdf-fill", "skill:same", "skill:nested"} {
		if _, ok := got[id]; ok {
			t.Fatalf("%s is read although AGENTGO_EXTERNAL_SKILLS is empty", id)
		}
	}

	t.Setenv("AGENTGO_EXTERNAL_SKILLS", "claude")
	got = load()
	if s := got["skill:pdf-fill"]; s.Scope != "claude" || s.Description != "Fill in PDF forms." {
		t.Errorf("pdf-fill: %+v", s)
	}
	if s := got["skill:same"]; s.Scope != "claude" || !strings.Contains(s.Content, "claude copy") {
		t.Errorf("same: %+v", s)
	}
	if _, ok := got["skill:nested"]; !ok {
		t.Error("a skill two levels down must be found")
	}
	for _, id := range []string{"skill:hidden", "skill:x", "skill:too-deep"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s must not be read", id)
		}
	}

	t.Setenv("AGENTGO_EXTERNAL_SKILLS", "all")
	if s := load()["skill:same"]; s.Scope != "agents" {
		t.Errorf("with all, .agents is first in line: %+v", s)
	}
	t.Setenv("AGENTGO_EXTERNAL_SKILLS", "claude,agents")
	if s := load()["skill:same"]; s.Scope != "claude" {
		t.Errorf("a list is in the order given: %+v", s)
	}
}

func TestSkillFoldersFollowSymlinksAndTheDirsFlag(t *testing.T) {
	home := fakeHome(t)
	real := filepath.Join(t.TempDir(), "elsewhere")
	writeSkill(t, real, "linked", "# linked\n\ninstalled by a skill manager.\n")
	link := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinked := os.Symlink(filepath.Join(real, "linked"), filepath.Join(link, "linked")) == nil

	extra := t.TempDir()
	writeSkill(t, extra, "from-dirs", "# from-dirs\n\nnamed in AGENTGO_SKILL_DIRS.\n")
	t.Setenv("AGENTGO_SKILL_DIRS", " "+extra+string(os.PathListSeparator)+string(os.PathListSeparator)+filepath.Join(extra, "missing"))
	t.Setenv("AGENTGO_EXTERNAL_SKILLS", "claude")

	l := NewLoader(t.TempDir())
	l.SetSources(DefaultSources(""))
	got := byID(l.Reload())
	if s := got["skill:from-dirs"]; s.Scope != ScopeExtra {
		t.Errorf("AGENTGO_SKILL_DIRS: %+v", s)
	}
	if symlinked {
		if s := got["skill:linked"]; s.Scope != "claude" {
			t.Errorf("a symlinked skill folder must be followed: %+v", s)
		}
	} else {
		t.Log("symlinks are not available here; skipped that half")
	}
	// Two folders from AGENTGO_SKILL_DIRS (blank entries dropped, a missing one kept) and one agent.
	if len(l.Sources()) != 3 {
		t.Errorf("sources: %+v", l.Sources())
	}
	l.SetSources(nil)
	if _, ok := byID(l.Reload())["skill:from-dirs"]; ok {
		t.Error("SetSources(nil) must drop the extra folders")
	}
}

func TestFrontMatterThatIsNotValidYAMLStillGivesNameAndDescription(t *testing.T) {
	cases := []struct{ name, doc, wantName, wantDesc string }{
		{
			"unquoted colon",
			"---\nname: pdf-fill\ndescription: Use when: the user mentions PDF forms\nallowed-tools: [Read]\n---\n# body\n",
			"pdf-fill", "Use when: the user mentions PDF forms",
		},
		{
			"quoted values next to a broken line",
			"---\nname: \"quoted-name\"\ndescription: 'Single quoted: still fine'\ntags: [unclosed\n---\n# body\n",
			"quoted-name", "Single quoted: still fine",
		},
		{
			"folded block next to a broken line",
			"---\nname: folded\ndescription: >-\n  first line\n  second line\ntags: [unclosed\n---\n# body\n",
			"folded", "first line second line",
		},
	}
	for _, c := range cases {
		m, body := SplitFrontMatter(c.doc)
		if m.Name != c.wantName || m.Description != c.wantDesc || body != "# body\n" {
			t.Errorf("%s: meta %+v body %q", c.name, m, body)
		}
	}
}
