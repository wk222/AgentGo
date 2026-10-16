package workflow

import (
	"regexp"
	"testing"
)

var validToolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func TestSafeToolName(t *testing.T) {
	// Plain ASCII keeps the historical mapping.
	for in, want := range map[string]string{
		"Happy Path":   "happy_path",
		"my-flow":      "my_flow",
		"data.import":  "data.import",
		"  Trim Me  ":  "trim_me",
		"":             "unnamed",
		"build_report": "build_report",
	} {
		if got := SafeToolName(in, ""); got != want {
			t.Errorf("SafeToolName(%q) = %q, want %q", in, got, want)
		}
	}

	// Anything else must be a valid LLM function name, and distinct inputs must stay distinct.
	seen := map[string]string{}
	for _, in := range []string{
		"预览 Happy Path", "预览 Happy Path 2", "日报生成", "周报生成", "flow (copy)", "flow [copy]",
		"a/b", "a:b", "emoji 🚀", "非常非常非常非常非常非常非常非常非常非常非常非常非常非常非常非常非常非常长的名字",
	} {
		got := SafeToolName(in, "wf-1")
		if !validToolName.MatchString(got) {
			t.Errorf("SafeToolName(%q) = %q is not a valid tool name", in, got)
		}
		if len(got) > toolNameMaxLen+9 {
			t.Errorf("SafeToolName(%q) = %q too long", in, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("collision: %q and %q -> %q", prev, in, got)
		}
		seen[got] = in
	}

	// No usable characters: fall back to id.
	if got := SafeToolName("日报", ""); !validToolName.MatchString(got) {
		t.Errorf("fallback = %q", got)
	}
	if got := SafeToolName("", "WF_42"); got != "wf_42" {
		t.Errorf("id fallback = %q", got)
	}
}
