package experts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// officialIDs are the experts that ship with the program. A change here is a
// product decision: add the file and the id together, or remove both.
var officialIDs = []string{
	"agentgo-guide", "automation-planner", "bug-hunter", "code-reviewer", "connector-engineer",
	"dashboard-builder", "data-analyst", "document-analyst", "financial-modeler", "office-assistant",
	"paper-writer", "pitch-deck-designer", "research-analyst", "skill-author", "software-engineer",
	"translator", "web-operator", "writing-editor",
}

const sample = `---
name: 测试专家
summary: 只用于测试。
category: Code
skills: [skill:alpha, beta, alpha]
starters:
  - 第一条
  - 第二条
---
这是规则正文，至少要有二十个字才算一份像样的专家说明书。
`

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func ids(list []Expert) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.ID
	}
	return out
}

func TestOfficialExpertsAreWellFormed(t *testing.T) {
	c := Load(Options{})
	if len(c.Warnings) > 0 {
		t.Fatalf("the built-in experts have problems: %v", c.Warnings)
	}
	got := map[string]Expert{}
	for _, e := range c.Experts {
		got[e.ID] = e
	}
	for _, id := range officialIDs {
		e, ok := got[id]
		if !ok {
			t.Errorf("official expert %q is missing", id)
			continue
		}
		if e.Source != SourceOfficial || e.Overrides != "" || e.Path != "" {
			t.Errorf("%s: source=%q overrides=%q path=%q", id, e.Source, e.Overrides, e.Path)
		}
		if len(e.Starters) < 2 {
			t.Errorf("%s: a picker card needs a few starter prompts, has %d", id, len(e.Starters))
		}
		if !strings.Contains(e.Rules, "## ") {
			t.Errorf("%s: rules have no sections", id)
		}
		if !contains(categoryOrder, e.Category) {
			t.Errorf("%s: category %q is not one the picker groups by (%v)", id, e.Category, categoryOrder)
		}
	}
	if len(c.Experts) != len(officialIDs) {
		t.Errorf("%d official experts, test lists %d: %v", len(c.Experts), len(officialIDs), ids(c.Experts))
	}
	names := map[string]string{}
	for _, e := range c.Experts {
		if other, dup := names[e.Name]; dup {
			t.Errorf("%s and %s share the name %q", e.ID, other, e.Name)
		}
		names[e.Name] = e.ID
	}
	if all := OfficialIDs(); len(all) != len(officialIDs) {
		t.Errorf("OfficialIDs() = %v", all)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestOfficialExpertsFollowTheirSwitches(t *testing.T) {
	t.Setenv("AGENTGO_OFFICEDOC", "0")
	t.Setenv("AGENTGO_DOC_INDEX", "0")
	t.Setenv("AGENTGO_BROWSER_CONTROL", "0")
	t.Setenv("AGENTGO_CODETOOLS", "0")
	have := map[string]bool{}
	for _, e := range Load(Options{}).Experts {
		have[e.ID] = true
	}
	for id := range builtinGates {
		if have[id] {
			t.Errorf("%s needs tools that are switched off, yet it is offered", id)
		}
	}
	for _, id := range []string{"code-reviewer", "bug-hunter", "research-analyst", "translator", "agentgo-guide", "automation-planner"} {
		if !have[id] {
			t.Errorf("%s has nothing to do with these switches", id)
		}
	}
	for id := range builtinGates {
		if !contains(officialIDs, id) {
			t.Errorf("gate for %q, which is not an official expert", id)
		}
	}
}

func TestOfficialExpertsNeedTheirKeyTool(t *testing.T) {
	// A host that cannot tell offers everything.
	if n := len(Load(Options{}).Experts); n != len(officialIDs) {
		t.Fatalf("%d experts without a tool check, want %d", n, len(officialIDs))
	}
	// A host without the browser tools loses exactly the expert built around them.
	noBrowser := func(name string) bool { return !strings.HasPrefix(name, "browser_") }
	got := Load(Options{HasTool: noBrowser})
	if _, ok := got.Get("web-operator"); ok {
		t.Error("web-operator is offered although the browser tools are missing")
	}
	if len(got.Experts) != len(officialIDs)-1 {
		t.Errorf("%d experts without browser tools, want %d: %v", len(got.Experts), len(officialIDs)-1, ids(got.Experts))
	}
	// Only the program's own experts are held to this: a user's file with that id is the user's call.
	dir := t.TempDir()
	write(t, dir, "web-operator.md", sample)
	mine, ok := Load(Options{UserDir: dir, HasTool: noBrowser}).Get("web-operator")
	if !ok || mine.Source != SourceUser {
		t.Errorf("the user's own web-operator must stay: %+v ok=%v", mine, ok)
	}
}

func TestKeyToolsAreNamedInTheRules(t *testing.T) {
	all := Load(Options{})
	for id, tool := range builtinNeeds {
		e, ok := all.Get(id)
		if !ok {
			t.Errorf("a key tool is listed for %q, which is not an official expert", id)
			continue
		}
		if !strings.Contains(e.Rules, "`"+tool+"`") {
			t.Errorf("%s: its key tool %s is not one its rules name", id, tool)
		}
	}
}

func TestParseReadsAFileAndCleansIt(t *testing.T) {
	e, warns, err := Parse("tester", sample)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if e.ID != "tester" || e.Name != "测试专家" || e.Summary != "只用于测试。" {
		t.Errorf("%+v", e)
	}
	if e.Category != "code" {
		t.Errorf("category is lower-cased: %q", e.Category)
	}
	if strings.Join(e.Skills, ",") != "alpha,beta" {
		t.Errorf("skills lose the prefix and repeats: %v", e.Skills)
	}
	if len(e.Starters) != 2 || e.Order != defaultOrder {
		t.Errorf("starters=%v order=%d", e.Starters, e.Order)
	}
	if !strings.HasPrefix(e.Rules, "这是规则正文") || strings.HasSuffix(e.Rules, "\n") {
		t.Errorf("rules = %q", e.Rules)
	}
}

func TestParseToleratesBOMAndCRLFAndAnEndingFence(t *testing.T) {
	text := "\ufeff" + strings.ReplaceAll(sample, "\n", "\r\n")
	if _, _, err := Parse("tester", text); err != nil {
		t.Fatalf("BOM + CRLF: %v", err)
	}
	// The closing fence is the last thing in the file: no rules, so the error is
	// about the rules, not about the front matter.
	_, _, err := Parse("tester", "---\nname: a\nsummary: b\n---")
	if err == nil || !strings.Contains(err.Error(), "规则正文太短") {
		t.Errorf("err = %v", err)
	}
}

func TestParseRefusesWhatCannotWork(t *testing.T) {
	body := "这是规则正文，至少要有二十个字才算一份像样的专家说明书。"
	long := func(n int) string { return strings.Repeat("字", n) }
	cases := []struct {
		name, id, text, want string
	}{
		{"bad id", "Bad Id", sample, "不能当专家 id"},
		{"no front matter", "x", body, "缺少 front matter"},
		{"unclosed", "x", "---\nname: a\nsummary: b\n" + body, "缺少 front matter"},
		{"not yaml", "x", "---\nname: [a\nsummary: b\n---\n" + body, "不是合法的 YAML"},
		{"missing name", "x", "---\nsummary: b\n---\n" + body, "缺少 name"},
		{"missing summary", "x", "---\nname: a\n---\n" + body, "缺少 summary"},
		{"multi-line name", "x", "---\nname: \"a\\nb\"\nsummary: s\n---\n" + body, "name 必须是单行"},
		{"long summary", "x", "---\nname: a\nsummary: " + long(MaxSummaryRunes+1) + "\n---\n" + body, "summary 有"},
		{"skills not a list", "x", "---\nname: a\nsummary: s\nskills: office\n---\n" + body, "字段类型不对"},
		{"too many skills", "x", "---\nname: a\nsummary: s\nskills: [a,b,c,d,e,f,g,h,i]\n---\n" + body, "skills 有 9 个"},
		{"too many starters", "x", "---\nname: a\nsummary: s\nstarters: [1,2,3,4,5,6,7]\n---\n" + body, "starters 有 7 条"},
		{"long starter", "x", "---\nname: a\nsummary: s\nstarters: [" + long(MaxStarterRunes+1) + "]\n---\n" + body, "starters 里的一条"},
		{"empty rules", "x", "---\nname: a\nsummary: s\n---\n", "规则正文太短"},
		{"huge rules", "x", "---\nname: a\nsummary: s\n---\n" + strings.Repeat("规", MaxRulesBytes), "超过上限"},
	}
	for _, c := range cases {
		_, _, err := Parse(c.id, c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestParseWarnsAboutUnknownKeysButLoads(t *testing.T) {
	text := strings.Replace(sample, "category: Code", "category: code\nstarter: typo\navatar: x.png", 1)
	e, warns, err := Parse("tester", text)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "tester" || len(warns) != 1 || !strings.Contains(warns[0], "avatar、starter") {
		t.Errorf("warns = %v", warns)
	}
}

func TestLoadLayersSourcesAndSaysWhatWasReplaced(t *testing.T) {
	user, ws := t.TempDir(), t.TempDir()
	write(t, user, "financial-modeler.md", strings.Replace(sample, "测试专家", "我的财务建模师", 1))
	write(t, user, "mine.md", strings.Replace(sample, "测试专家", "我自己的", 1))
	write(t, ws, "mine.md", strings.Replace(sample, "测试专家", "项目里的", 1))
	write(t, ws, "only-here.md", sample)

	c := Load(Options{UserDir: user, WorkspaceDir: ws})
	if len(c.Warnings) != 0 {
		t.Fatalf("warnings: %v", c.Warnings)
	}
	got := map[string]Expert{}
	for _, e := range c.Experts {
		got[e.ID] = e
	}
	if e := got["financial-modeler"]; e.Name != "我的财务建模师" || e.Source != SourceUser || e.Overrides != SourceOfficial || e.Path == "" {
		t.Errorf("a user file replaces the official expert: %+v", e)
	}
	if e := got["mine"]; e.Name != "项目里的" || e.Source != SourceWorkspace || e.Overrides != SourceUser {
		t.Errorf("the workspace replaces the user's: %+v", e)
	}
	if e := got["only-here"]; e.Source != SourceWorkspace || e.Overrides != "" {
		t.Errorf("%+v", e)
	}
	if len(c.Experts) != len(officialIDs)+2 {
		t.Errorf("%d experts: %v", len(c.Experts), ids(c.Experts))
	}
}

func TestLoadSkipsBadFilesAndExplainsEach(t *testing.T) {
	user := t.TempDir()
	write(t, user, "good.md", sample)
	write(t, user, "Bad Name.md", sample)       // not a usable id
	write(t, user, "no-front.md", "只有正文，没有头部。") // no front matter
	write(t, user, "_draft.md", sample)         // drafts are skipped quietly
	write(t, user, ".hidden.md", sample)
	write(t, user, "notes.txt", sample)
	write(t, user, "huge.md", strings.Repeat("x", maxFileBytes+1))
	if err := os.MkdirAll(filepath.Join(user, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := Load(Options{UserDir: user})
	have := map[string]bool{}
	for _, e := range c.Experts {
		have[e.ID] = true
	}
	if !have["good"] || have["_draft"] || have[".hidden"] || have["notes"] || have["sub"] {
		t.Errorf("experts: %v", ids(c.Experts))
	}
	joined := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"Bad Name.md", "不能当专家 id", "no-front.md", "缺少 front matter", "huge.md", "不像一份专家说明"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should mention %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "_draft") || strings.Contains(joined, "notes.txt") {
		t.Errorf("skipped files are not worth a warning:\n%s", joined)
	}
	if len(c.Warnings) != 3 {
		t.Errorf("want one warning per bad file, got %d:\n%s", len(c.Warnings), joined)
	}
}

func TestLoadReadsEditedFilesAtOnceAndOtherEncodings(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "tester.md", sample)
	if e, _ := Load(Options{UserDir: dir}).Get("tester"); e.Name != "测试专家" {
		t.Fatalf("first read: %+v", e)
	}
	write(t, dir, "tester.md", strings.Replace(sample, "测试专家", "改过的专家", 1))
	if e, _ := Load(Options{UserDir: dir}).Get("tester"); e.Name != "改过的专家" {
		t.Errorf("an edit is picked up on the next Load: %+v", e)
	}

	// Windows PowerShell's Out-File writes UTF-16 with a BOM.
	u := utf16.Encode([]rune(sample))
	raw := []byte{0xFF, 0xFE}
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Load(Options{UserDir: dir})
	if e, ok := c.Get("tester"); !ok || e.Name != "测试专家" {
		t.Errorf("UTF-16: %+v warnings=%v", e, c.Warnings)
	}
}

func TestLoadIgnoresMissingFolders(t *testing.T) {
	c := Load(Options{UserDir: filepath.Join(t.TempDir(), "nope"), WorkspaceDir: "  "})
	if len(c.Warnings) != 0 || len(c.Experts) != len(officialIDs) {
		t.Errorf("warnings=%v experts=%d", c.Warnings, len(c.Experts))
	}
}

func TestLoadOrdersByCategoryThenOrderThenID(t *testing.T) {
	dir := t.TempDir()
	mk := func(id, category string, order int) {
		text := "---\nname: " + id + "\nsummary: s\ncategory: " + category + "\norder: " + strconv.Itoa(order) + "\n---\n这是规则正文，至少要有二十个字才算一份像样的专家说明书。\n"
		write(t, dir, id+".md", text)
	}
	mk("zz-first", "zeta", 1)
	mk("aa-late", "alpha", 50)
	mk("bb-early", "alpha", 5)
	mk("cc-early", "alpha", 5)

	var got []string
	for _, e := range Load(Options{UserDir: dir}).Experts {
		if e.Source == SourceUser {
			got = append(got, e.ID)
		}
	}
	if want := "bb-early cc-early aa-late zz-first"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
	all := Load(Options{UserDir: dir}).Experts
	if all[0].Category != "office" || all[len(all)-1].ID != "zz-first" {
		t.Errorf("known categories come first in the picker's order, unknown ones last: first=%s last=%s", all[0].ID, all[len(all)-1].ID)
	}
}

func TestGetFindsByIDNameOrSpelledOutID(t *testing.T) {
	c := Load(Options{})
	for _, key := range []string{"financial-modeler", " 财务建模师 ", "Financial Modeler", "financial_modeler", "FINANCIAL-MODELER"} {
		e, ok := c.Get(key)
		if !ok || e.ID != "financial-modeler" {
			t.Errorf("Get(%q) = %v, %v", key, e.ID, ok)
		}
	}
	if _, ok := c.Get(""); ok {
		t.Error("an empty key matches nothing")
	}
	_, err := c.Find("会计")
	if err == nil || !strings.Contains(err.Error(), "financial-modeler（财务建模师）") {
		t.Errorf("a miss lists what exists: %v", err)
	}
	if _, err := c.Find(" "); err == nil || !strings.Contains(err.Error(), "没有指定") {
		t.Errorf("err = %v", err)
	}
}

func TestPromptHasTheRulesAndNamesOnlySkillsThatExist(t *testing.T) {
	e, _, err := Parse("tester", sample)
	if err != nil {
		t.Fatal(err)
	}
	p := e.Prompt(func(s string) bool { return s == "beta" })
	for _, want := range []string{"[Expert: 测试专家 (tester)]", "never replace them", "Summary: 只用于测试。", "这是规则正文", "skill:beta"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "skill:alpha") {
		t.Errorf("a skill that is not there must not be named:\n%s", p)
	}
	if none := e.Prompt(func(string) bool { return false }); strings.Contains(none, "Skills this expert relies on") {
		t.Errorf("no skills, no skills line:\n%s", none)
	}
	if all := e.Prompt(nil); !strings.Contains(all, "skill:alpha, skill:beta") {
		t.Errorf("a nil check names them all:\n%s", all)
	}
	if len(p) > MaxRulesBytes+1000 {
		t.Errorf("the block is %d bytes: the shell around the rules should stay small", len(p))
	}
}
