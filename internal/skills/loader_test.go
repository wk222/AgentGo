package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// officeSkills follow the office switch; the router comes first and names the rest.
var officeSkills = []string{
	"office", "office-dashboard", "office-docx", "office-financial-model", "office-paper",
	"office-pitch-deck", "office-pptx", "office-report", "office-xlsx",
}

// otherBuiltins are the built-in skills that do not belong to the office group.
var otherBuiltins = []string{"agentgo-doctor", "document-qa", "mcp-connect", "skill-writer"}

func allBuiltins() []string {
	return append(append([]string{}, officeSkills...), otherBuiltins...)
}

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
	if len(got) != len(allBuiltins()) {
		t.Errorf("%d skills loaded, the test knows %d built-in ones: a new skill must be added to officeSkills or otherBuiltins", len(got), len(allBuiltins()))
	}
	for _, name := range allBuiltins() {
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

// skill:office is the entry, but nothing forces the model through it: one that goes straight to a
// detailed office skill must still be told about the scenario skill that fits the job.
func TestDetailedOfficeSkillsPointToTheirScenarioSkills(t *testing.T) {
	got := byID(NewLoader(t.TempDir()).Reload())
	for from, to := range map[string][]string{
		"office-docx": {"office-paper"},
		"office-pptx": {"office-pitch-deck"},
		"office-xlsx": {"office-dashboard", "office-financial-model"},
	} {
		for _, name := range to {
			if !strings.Contains(got["skill:"+from].Content, "skill:"+name) {
				t.Errorf("skill:%s does not point to skill:%s", from, name)
			}
		}
		// The pointer is not the first paragraph: that one is the trigger the catalog shows.
		if d := got["skill:"+from].Description; strings.Contains(d, "skill:") {
			t.Errorf("skill:%s: the pointer leaked into the description: %q", from, d)
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
	for _, name := range otherBuiltins {
		if _, ok := got["skill:"+name]; !ok {
			t.Errorf("skill %q has nothing to do with the office switch", name)
		}
	}
}

func TestDocumentQAFollowsTheDocumentIndexSwitch(t *testing.T) {
	t.Setenv("AGENTGO_DOC_INDEX", "0")
	got := byID(NewLoader(t.TempDir()).Reload())
	if _, ok := got["skill:document-qa"]; ok {
		t.Error("document-qa describes tools that are switched off")
	}
	for _, name := range officeSkills {
		if _, ok := got["skill:"+name]; !ok {
			t.Errorf("skill %q has nothing to do with the document index", name)
		}
	}
}

var (
	// `skill:name`; a placeholder such as `skill:<名字>` is not a reference.
	skillRef = regexp.MustCompile("`skill:([a-z][a-z0-9-]*)`")
	// A bare `name` that looks like one of the built-in skills' families. Other
	// hyphenated words are CSS properties, date formats and the like.
	kebabCode = regexp.MustCompile("`((?:office|document|skill|mcp|agentgo)-[a-z-]+)`")
)

// A built-in skill that sends the model to another skill must name one that exists,
// whether it writes `skill:name` or just `name`.
func TestBuiltinSkillsPointOnlyToSkillsThatExist(t *testing.T) {
	got := byID(NewLoader(t.TempDir()).Reload())
	for _, name := range allBuiltins() {
		text := got["skill:"+name].Content
		for _, m := range skillRef.FindAllStringSubmatch(text, -1) {
			if _, ok := got["skill:"+m[1]]; !ok {
				t.Errorf("%s points to `skill:%s`, which is not a built-in skill", name, m[1])
			}
		}
		for _, m := range kebabCode.FindAllStringSubmatch(text, -1) {
			if _, ok := got["skill:"+m[1]]; !ok {
				t.Errorf("%s mentions `%s`, which is not a built-in skill (and is not a tool or option name)", name, m[1])
			}
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
