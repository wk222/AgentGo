package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/skills"
)

func activateSkillTool(t *testing.T, ws string) einotool.InvokableTool {
	t.Helper()
	r := NewRegistry()
	if err := RegisterActivateSkill(r, skills.NewLoader(ws)); err != nil {
		t.Fatal(err)
	}
	bt, ok := r.Get("activate_skill")
	if !ok {
		t.Fatal("activate_skill not registered")
	}
	return bt.(einotool.InvokableTool)
}

func activate(t *testing.T, tl einotool.InvokableTool, id string) activateSkillOutput {
	t.Helper()
	raw, err := tl.InvokableRun(context.Background(), `{"skill_id":`+quoteJSON(id)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	var out activateSkillOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bad output %q: %v", raw, err)
	}
	return out
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestActivateSkillReturnsTheSkillText(t *testing.T) {
	tl := activateSkillTool(t, t.TempDir())
	info, _ := tl.Info(context.Background())
	if !strings.Contains(info.Desc, "skill:office") || !strings.Contains(info.Desc, "skill:office-pptx") {
		t.Errorf("the description must name the office skills so the model finds them:\n%s", info.Desc)
	}

	for _, id := range []string{"skill:office-pptx", "office-pptx", " skill:office-pptx "} {
		out := activate(t, tl, id)
		if !strings.Contains(out.Message, "skill loaded: office-pptx") || !strings.Contains(out.Content, "create_pptx") {
			t.Errorf("%q: message %q, content %.60q", id, out.Message, out.Content)
		}
		if out.Available != nil {
			t.Errorf("%q: a found skill lists no alternatives", id)
		}
	}
}

func writeSkill(t *testing.T, ws, name, body string) {
	t.Helper()
	dir := filepath.Join(ws, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The model decides from the tool description alone whether a skill applies, so the
// description says when each one is for, puts the user's own first, and does not
// list the detailed office skills one by one (skill:office names them).
func TestActivateSkillDescriptionSaysWhenEachSkillApplies(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "weekly-report", "# weekly-report\n\n用户要做销售周报时先读本技能：先导出明细，再汇总。\n")
	tl := activateSkillTool(t, ws)
	info, _ := tl.Info(context.Background())
	d := info.Desc

	own := strings.Index(d, "skill:weekly-report (用户要做销售周报)")
	shipped := strings.Index(d, "skill:agentgo-doctor (")
	if own < 0 || shipped < 0 || own > shipped {
		t.Errorf("own skills come first, with their trigger (own at %d, built-in at %d):\n%s", own, shipped, d)
	}
	for _, want := range []string{"skill:document-qa (", "skill:mcp-connect (", "skill:skill-writer (", "skill:office ("} {
		if !strings.Contains(d, want) {
			t.Errorf("the description lacks %s\n%s", want, d)
		}
	}
	for _, left := range []string{"skill:office-dashboard", "skill:office-paper"} {
		// skill:office-xlsx, -pptx and -docx are named once, in the sentence about office files.
		if strings.Contains(d, left+" (") {
			t.Errorf("%s should be reached through skill:office, not listed on its own", left)
		}
	}
	if strings.Contains(d, "时先读本技能") {
		t.Errorf("the trigger stops before the boilerplate:\n%s", d)
	}
	if n := len(d); n > 3000 {
		t.Errorf("the description is %d bytes, which every request pays for", n)
	}
	// Deterministic: the same skills give the same text.
	again, _ := activateSkillTool(t, ws).Info(context.Background())
	if again.Desc != d {
		t.Error("the description changes between registrations")
	}
}

func TestActivateSkillDescriptionCapsTheUsersOwnSkills(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 20; i++ {
		writeSkill(t, ws, fmt.Sprintf("s%02d", i), fmt.Sprintf("# s%02d\n\n第 %d 号技能。\n", i, i))
	}
	info, _ := activateSkillTool(t, ws).Info(context.Background())
	if !strings.Contains(info.Desc, "skill:s11 (") || strings.Contains(info.Desc, "skill:s12 (") {
		t.Errorf("exactly the first %d own skills are listed:\n%s", maxOwnSkillsListed, info.Desc)
	}
	if !strings.Contains(info.Desc, "and 8 more") {
		t.Errorf("the rest is counted:\n%s", info.Desc)
	}
	if !strings.Contains(info.Desc, "skill:agentgo-doctor (") {
		t.Error("a long list of own skills must not push the built-in ones out")
	}
}

func TestSkillTriggerStopsAtTheWhenClause(t *testing.T) {
	for in, want := range map[string]string{
		"用户要做看板时先读本技能：成品是 xlsx。":           "用户要做看板",
		"Build the weekly report. Needs python.": "Build the weekly report. Needs python.",
		"周报：怎么做":                                "周报",
		"  多个   空格\n换行 ":                          "多个 空格 换行",
		"":                                      "",
	} {
		if got := skillTrigger(in); got != want {
			t.Errorf("skillTrigger(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("长", 100)
	if got := skillTrigger(long); len([]rune(got)) != triggerRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long trigger is cut to %d characters and marked: %q", triggerRunes, got)
	}
}

func TestActivateSkillListsWhatExistsWhenTheIDIsWrong(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "skills", "local-rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# local-rules\n\n本单位规则。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := activateSkillTool(t, ws)
	out := activate(t, tl, "skill:missing")
	if !strings.Contains(out.Message, "skill not found: skill:missing") || out.Content != "" {
		t.Errorf("out = %+v", out)
	}
	have := strings.Join(out.Available, ",")
	for _, want := range []string{"skill:local-rules", "skill:office", "skill:office-xlsx"} {
		if !strings.Contains(have, want) {
			t.Errorf("available %q lacks %s", have, want)
		}
	}
	// A skill added after startup is found without re-registering the tool.
	late := filepath.Join(ws, "skills", "late")
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(late, "SKILL.md"), []byte("# late\n\n后来加的。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := activate(t, tl, "late"); !strings.Contains(out.Content, "后来加的") {
		t.Errorf("late skill = %+v", out)
	}
}
