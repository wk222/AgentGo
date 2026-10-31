package tools

import (
	"context"
	"encoding/json"
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
