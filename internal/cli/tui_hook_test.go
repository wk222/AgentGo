package cli

import (
	"strings"
	"testing"
)

func mainWithTUI(h *harness, args []string, tty bool, hook func(Backend, TUIOptions) int) int {
	env := IO{In: strings.NewReader(""), Out: &h.out, Err: &h.err, InTTY: tty, TUI: hook}
	return Main(args, env, func(Config) (Backend, error) { return h.be, nil })
}

func TestInteractiveChatOnATerminalUsesTheTUI(t *testing.T) {
	h := newHarness()
	var got TUIOptions
	code := mainWithTUI(h, []string{"-w", "D:/proj", "-yolo", "-thinking", "-session", "s9"}, true,
		func(_ Backend, o TUIOptions) int { got = o; return 7 })
	if code != 7 {
		t.Fatalf("exit code %d, want the hook's 7", code)
	}
	if got.Workspace != "D:/proj" || got.Session != "s9" || !got.Yolo || !got.Thinking {
		t.Fatalf("options = %+v", got)
	}
}

func TestPlainJSONAndPipesSkipTheTUI(t *testing.T) {
	hook := func(Backend, TUIOptions) int { t.Fatal("TUI must not start"); return 0 }
	for _, c := range []struct {
		args []string
		tty  bool
	}{
		{[]string{"-plain"}, true},
		{[]string{"-json"}, true},
		{nil, false}, // stdin is not a terminal
	} {
		h := newHarness()
		if code := mainWithTUI(h, c.args, c.tty, hook); code != ExitOK {
			t.Errorf("args %v tty=%v: exit %d, stderr %s", c.args, c.tty, code, h.err.String())
		}
	}
}

func TestTUICommandNeedsATerminal(t *testing.T) {
	h := newHarness()
	if code := mainWithTUI(h, []string{"tui"}, false, func(Backend, TUIOptions) int { return 0 }); code != ExitUsage {
		t.Fatalf("exit %d, want usage error", code)
	}
	if !strings.Contains(h.err.String(), "interactive terminal") {
		t.Fatalf("stderr = %q", h.err.String())
	}
}
