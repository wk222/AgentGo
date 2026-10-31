package userhooks

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const claudeShaped = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|Edit", "hooks": [
        {"type": "command", "command": "python guard.py", "timeout": 5, "onFailure": "block"},
        {"type": "command", "command": "python second.py"}
      ]},
      {"matcher": "", "hooks": [{"command": "audit.cmd"}]}
    ],
    "PostToolUse": [{"matcher": "code_.*", "hooks": [{"type": "command", "command": "format.cmd", "timeout": 2.5}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "context.cmd"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "notify.cmd"}]}]
  }
}`

func TestParseReadsClaudesShape(t *testing.T) {
	cfg, problems, err := Parse([]byte(claudeShaped))
	if err != nil || len(problems) != 0 {
		t.Fatalf("err=%v problems=%v", err, problems)
	}
	if got := cfg.Counts(); got[PreToolUse] != 3 || got[PostToolUse] != 1 || got[UserPromptSubmit] != 1 || got[Stop] != 1 || cfg.Count() != 6 {
		t.Fatalf("counts = %v (total %d)", got, cfg.Count())
	}
	pre := cfg.For(PreToolUse)
	if pre[0].Command != "python guard.py" || pre[0].Matcher != "Bash|Edit" || pre[0].Timeout != 5*time.Second || pre[0].OnFailure != OnFailureBlock {
		t.Errorf("first hook = %+v", pre[0])
	}
	if pre[1].Command != "python second.py" || pre[1].Timeout != DefaultTimeout || pre[1].OnFailure != OnFailureContinue {
		t.Errorf("a hook with no timeout or onFailure must get the defaults: %+v", pre[1])
	}
	if pre[2].Command != "audit.cmd" || !pre[2].match.matches([]string{"anything"}) {
		t.Errorf("an empty matcher applies to every tool: %+v", pre[2])
	}
	if post := cfg.For(PostToolUse)[0]; post.Timeout != 2500*time.Millisecond {
		t.Errorf("fractional seconds: %v", post.Timeout)
	}
	if l := pre[0].Label(); !strings.Contains(l, "PreToolUse[Bash|Edit]") || !strings.Contains(l, "python guard.py") {
		t.Errorf("label = %q", l)
	}
}

func TestParseAcceptsABOMAndABareTopLevel(t *testing.T) {
	withBOM := "\uFEFF" + claudeShaped // Notepad saves UTF-8 files like this
	if cfg, problems, err := Parse([]byte(withBOM)); err != nil || cfg.Count() != 6 || len(problems) != 0 {
		t.Fatalf("BOM: count=%d problems=%v err=%v", cfg.Count(), problems, err)
	}
	bare := `{"PreToolUse": [{"matcher": "Bash", "hooks": [{"command": "x.cmd"}]}], "disableAllHooks": false}`
	if cfg, problems, err := Parse([]byte(bare)); err != nil || cfg.Count() != 1 || len(problems) != 0 {
		t.Fatalf("bare: count=%d problems=%v err=%v", cfg.Count(), problems, err)
	}
	off := `{"disableAllHooks": true, "hooks": {"Stop": [{"hooks": [{"command": "x.cmd"}]}]}}`
	cfg, problems, err := Parse([]byte(off))
	if err != nil || cfg.Count() != 0 || len(problems) != 1 || !strings.Contains(problems[0].Message, "switched off") {
		t.Fatalf("disableAllHooks: count=%d problems=%v err=%v", cfg.Count(), problems, err)
	}
}

func TestParseEmptyFileAndEmptyHooksAreFine(t *testing.T) {
	for _, text := range []string{"", "  \r\n ", "{}", `{"hooks": {}}`, `{"hooks": null}`} {
		cfg, problems, err := Parse([]byte(text))
		if err != nil || cfg.Count() != 0 || len(problems) != 0 {
			t.Errorf("%q: count=%d problems=%v err=%v", text, cfg.Count(), problems, err)
		}
	}
	// something that is neither: say so instead of silently doing nothing
	_, problems, err := Parse([]byte(`{"hook": {"Stop": []}}`))
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "no hooks found") {
		t.Errorf("a misspelled top-level key should be reported: %v %v", problems, err)
	}
}

func TestParseTextThatIsNotJSONIsAnError(t *testing.T) {
	for _, text := range []string{"{nope", `["a"]`, `{"hooks": []}`, `{"hooks": "x"}`} {
		if _, _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%q should not parse", text)
		}
	}
}

func TestParseLeavesOutWhatCannotBeUsedAndSaysWhere(t *testing.T) {
	text := `{"hooks": {
	  "SessionStart": [{"hooks": [{"command": "a.cmd"}]}],
	  "PreToolUse": [
	    {"matcher": "(", "hooks": [{"command": "bad-matcher.cmd"}]},
	    {"matcher": "Bash", "hooks": [
	      {"type": "http", "command": "http-hook"},
	      {"type": "command", "command": "   "},
	      {"type": "command", "command": "slow.cmd", "timeout": 9999, "onFailure": "maybe", "async": true},
	      {"type": "command", "command": "good.cmd"}
	    ]}
	  ],
	  "Stop": {"not": "a list"},
	  "UserPromptSubmit": [{"matcher": "Bash", "hooks": [{"command": "ctx.cmd"}]}]
	}}`
	cfg, problems, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.For(PreToolUse); len(got) != 2 || got[0].Command != "slow.cmd" || got[1].Command != "good.cmd" {
		t.Fatalf("usable PreToolUse hooks = %+v", got)
	}
	if slow := cfg.For(PreToolUse)[0]; slow.Timeout != MaxTimeout || slow.OnFailure != OnFailureContinue {
		t.Errorf("a too-long timeout is clamped and a bad onFailure falls back to continue: %+v", slow)
	}
	if cfg.Count() != 3 || len(cfg.For(Stop)) != 0 || len(cfg.For(UserPromptSubmit)) != 1 {
		t.Errorf("counts = %v", cfg.Counts())
	}
	all := ""
	for _, p := range problems {
		all += p.String() + "\n"
	}
	for _, want := range []string{
		`hooks.SessionStart: event "SessionStart" is not supported`,
		"hooks.PreToolUse[0].matcher: is not a valid regular expression",
		`hooks.PreToolUse[1].hooks[0].type: type "http" is not supported`,
		"hooks.PreToolUse[1].hooks[1].command: is empty",
		"hooks.PreToolUse[1].hooks[2].timeout: is longer than 600 seconds",
		`hooks.PreToolUse[1].hooks[2].onFailure: "maybe" is not`,
		"hooks.PreToolUse[1].hooks[2].async: is not supported yet",
		"hooks.Stop: expected a list",
		"hooks.UserPromptSubmit[0].matcher: UserPromptSubmit has no tool name to match",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("problems lack %q:\n%s", want, all)
		}
	}
}

func TestParseCapsTheHooksOfOneEvent(t *testing.T) {
	var hooks []string
	for i := 0; i < MaxHooksPerEvent+5; i++ {
		hooks = append(hooks, fmt.Sprintf(`{"command": "h%d.cmd"}`, i))
	}
	text := `{"hooks": {"Stop": [{"hooks": [` + strings.Join(hooks, ",") + `]}]}}`
	cfg, problems, err := Parse([]byte(text))
	if err != nil || len(cfg.For(Stop)) != MaxHooksPerEvent {
		t.Fatalf("kept %d hooks, err=%v", len(cfg.For(Stop)), err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "the rest are left out") {
		t.Errorf("problems = %v", problems)
	}
}

func TestMatcherRules(t *testing.T) {
	bash := []string{"execute_bash", "Bash"} // a tool's own name, then the names it is known by
	for _, tc := range []struct {
		matcher string
		names   []string
		want    bool
	}{
		{"", []string{"anything"}, true},
		{"*", []string{"anything"}, true},
		{"Bash", bash, true},
		{"Bash", []string{"code_view", "Read"}, false},
		{"Bash|Edit", []string{"code_edit", "Edit"}, true},
		{"execute_bash", bash, true},
		{"bash", bash, false}, // names are case sensitive
		{"view", []string{"code_view"}, false},
		{"code_.*", []string{"code_view"}, true},
		{"^code_(view|edit)$", []string{"code_edit"}, true},
		{"^code_(view|edit)$", []string{"code_write"}, false},
		{"mcp__.*__search", []string{"mcp__web__search"}, true},
		{"Bash|", []string{"x"}, false},
	} {
		m, err := compileMatcher(tc.matcher)
		if err != nil {
			t.Errorf("%q: %v", tc.matcher, err)
			continue
		}
		if got := m.matches(tc.names); got != tc.want {
			t.Errorf("matcher %q on %v = %v, want %v", tc.matcher, tc.names, got, tc.want)
		}
	}
	if _, err := compileMatcher("a("); err == nil {
		t.Error("an invalid regular expression must be reported")
	}
}
