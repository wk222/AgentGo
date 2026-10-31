package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var officeSkills = []string{"office", "office-docx", "office-pptx", "office-report", "office-xlsx"}

func byID(list []Skill) map[string]Skill {
	m := map[string]Skill{}
	for _, s := range list {
		m[s.ID] = s
	}
	return m
}

func TestBuiltinSkillsComeWithTheProgram(t *testing.T) {
	// An empty workspace: nothing to read from disk, yet the office skills exist.
	got := byID(NewLoader(t.TempDir()).Reload())
	for _, name := range officeSkills {
		s, ok := got["skill:"+name]
		if !ok {
			t.Fatalf("builtin skill %q missing; have %v", name, got)
		}
		if s.Scope != ScopeBuiltin || s.Name != name || !strings.HasPrefix(s.Path, "builtin:") {
			t.Errorf("%s = scope %q name %q path %q", name, s.Scope, s.Name, s.Path)
		}
		if n := len([]rune(s.Description)); n < 20 || n > 400 {
			t.Errorf("%s: description of %d chars is not a usable trigger: %q", name, n, s.Description)
		}
		if !strings.HasPrefix(s.Content, "# "+name+"\n") {
			t.Errorf("%s: content must start with its heading, got %.30q", name, s.Content)
		}
		if strings.Contains(s.Content, "\r") {
			t.Errorf("%s: carriage returns in the content", name)
		}
		if len(s.Content) > MaxContextBytes {
			t.Errorf("%s: %d bytes would be cut when injected (cap %d)", name, len(s.Content), MaxContextBytes)
		}
	}
	// The router names the others so the model can find its way.
	for _, name := range officeSkills[1:] {
		if !strings.Contains(got["skill:office"].Content, "skill:"+name) {
			t.Errorf("the office skill does not point to skill:%s", name)
		}
	}
}

func TestWorkspaceSkillReplacesABuiltinOne(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "skills", "office-pptx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "# office-pptx\n\n本单位的 PPT 规范：统一用红色主题。\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(ws)
	got := byID(l.Reload())
	s := got["skill:office-pptx"]
	if s.Scope != "workspace" || s.Content != custom {
		t.Fatalf("workspace skill must win: %+v", s)
	}
	if got["skill:office-xlsx"].Scope != ScopeBuiltin {
		t.Error("the other builtin skills must stay")
	}
	if g, ok := l.Get("skill:office-pptx"); !ok || g.Scope != "workspace" {
		t.Errorf("Get = %+v %v", g, ok)
	}
}

func TestOfficeSkillsFollowTheOfficeSwitch(t *testing.T) {
	t.Setenv("AGENTGO_OFFICEDOC", "0")
	got := byID(NewLoader(t.TempDir()).Reload())
	for _, name := range officeSkills {
		if _, ok := got["skill:"+name]; ok {
			t.Errorf("skill %q describes tools that are switched off", name)
		}
	}
}

func TestContextBlockNeverSplitsACharacter(t *testing.T) {
	l := NewLoader(t.TempDir())
	l.skills["skill:long"] = Skill{ID: "skill:long", Name: "long", Content: strings.Repeat("汉字", MaxContextBytes)}
	l.skills["skill:short"] = Skill{ID: "skill:short", Name: "short", Content: "短内容"}
	block := l.ContextBlock([]string{"skill:long", "skill:short", "skill:nope"})
	if !utf8.ValidString(block) {
		t.Fatal("the cut split a multi-byte character")
	}
	if !strings.Contains(block, "(truncated)") || !strings.Contains(block, "短内容") || strings.Contains(block, "nope") {
		t.Errorf("block = %.80q …", block)
	}
	if got := ClipUTF8("汉字", 4); got != "汉" {
		t.Errorf("ClipUTF8 = %q", got)
	}
	if got := ClipUTF8("abc", 10); got != "abc" {
		t.Errorf("ClipUTF8 = %q", got)
	}
}
