package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentgo/internal/governance"
	"agentgo/internal/userhooks"
)

// hookScripts are command lines for the platform shell that stand in for the
// scripts a user would write.
func refuseWith(reason string) string {
	if runtime.GOOS == "windows" {
		return "echo " + reason + " 1>&2 & exit /b 2"
	}
	return "echo '" + reason + "' >&2; exit 2"
}

func copyStdinTo(path string) string {
	if runtime.GOOS == "windows" {
		return `more > "` + path + `"`
	}
	return `cat > "` + path + `"`
}

func writeUserHooks(t *testing.T, rt *Runtime, event, matcher, command string) {
	t.Helper()
	text, err := json.Marshal(map[string]any{"hooks": map[string]any{event: []any{
		map[string]any{"matcher": matcher, "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 20}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.dataDir, userHooksFile), text, 0o644); err != nil {
		t.Fatal(err)
	}
	rt.hooksEngine().Reload() // the engine looks at the file every 300 ms; do not wait for that here
}

// guarded runs one call of a free tool (no approval needed) through the same
// governance path the agent's tools take, and reports whether the tool itself ran.
func guarded(t *testing.T, tool string) (resp string, ran bool) {
	t.Helper()
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	mw := governance.NewGovernanceMiddleware(queue, governance.BuildPolicy("balanced", t.TempDir()))
	ctx := governance.WithSessionID(context.Background(), "sess-hooks")
	resp, err = mw.InvokeWithPolicy(ctx, tool, `{"file_path":"a.txt"}`, func(context.Context, string) (string, error) {
		ran = true
		return "file contents: hello", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp, ran
}

func TestUserHooksGuardToolCallsOfTheRunningApp(t *testing.T) {
	rt := boot(t)
	if rt.hooksEngine() == nil {
		t.Fatal("the userhooks plugin did not start the engine")
	}
	if st := rt.hooksEngine().Status(); st.Path != filepath.Join(rt.dataDir, userHooksFile) || st.Exists || !st.Enabled {
		t.Fatalf("status before any file exists = %+v", st)
	}

	if _, ran := guarded(t, "code_view"); !ran {
		t.Fatal("without a hooks.json the tool must run")
	}
	if n := len(rt.hooksEngine().Status().Recent); n != 0 {
		t.Fatalf("no hooks.json, yet %d script(s) ran", n)
	}

	// Claude's name for the reader tool selects ours; the script refuses.
	writeUserHooks(t, rt, userhooks.PreToolUse, "Read", refuseWith("reading is closed today"))
	resp, ran := guarded(t, "code_view")
	if ran {
		t.Fatal("a hook that refused did not stop the tool")
	}
	var shown struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(resp), &shown); err != nil {
		t.Fatalf("the model gets %q: %v", resp, err)
	}
	if shown.Status != "rejected" || !strings.Contains(shown.Message, "blocked by user hook: reading is closed today") {
		t.Errorf("what the model is told = %+v", shown)
	}
	if runs := rt.hooksEngine().Status().Recent; len(runs) != 1 || runs[0].Result != "blocked" || runs[0].Tool != "code_view" {
		t.Errorf("records = %+v", runs)
	}

	// The same hook leaves every other tool alone.
	if _, ran := guarded(t, "code_glob"); !ran {
		t.Error("a hook for Read stopped another tool")
	}
}

func TestUserHooksSeeTheResultOfAToolThatRan(t *testing.T) {
	rt := boot(t)
	seen := filepath.Join(t.TempDir(), "seen.json")
	writeUserHooks(t, rt, userhooks.PostToolUse, "code_.*", copyStdinTo(seen))

	if _, ran := guarded(t, "code_view"); !ran {
		t.Fatal("a PostToolUse hook must not stop the tool")
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the script did not run: %v (records %+v)", err, rt.hooksEngine().Status().Recent)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &got); err != nil {
		t.Fatalf("stdin = %q: %v", raw, err)
	}
	if got["hook_event_name"] != "PostToolUse" || got["tool_name"] != "code_view" || got["session_id"] != "sess-hooks" ||
		got["tool_response"] != "file contents: hello" {
		t.Errorf("stdin = %s", raw)
	}
	if input, _ := got["tool_input"].(map[string]any); input["file_path"] != "a.txt" {
		t.Errorf("tool_input = %v", got["tool_input"])
	}
}

func TestUserHooksCanBeSwitchedOff(t *testing.T) {
	t.Setenv("AGENTGO_USER_HOOKS", "0")
	rt := boot(t)
	if rt.hooksEngine() != nil {
		t.Fatal("AGENTGO_USER_HOOKS=0 must not start the engine")
	}
	writeUserHooks(t, rt, userhooks.PreToolUse, "", refuseWith("no"))
	if _, ran := guarded(t, "code_view"); !ran {
		t.Fatal("hooks are switched off, yet the tool was stopped")
	}
}

func TestUserHooksStopWhenTheRuntimeCloses(t *testing.T) {
	t.Setenv("AGENTGO_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("AGENTGO_GATEWAY_ADDR", "")
	t.Setenv("AGENTGO_GATEWAY_PORT", "")
	t.Setenv(pluginsDisabledEnv, "")
	rt, err := newRuntimeWith(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = rt.Close()
		}
	})
	writeUserHooks(t, rt, userhooks.PreToolUse, "", refuseWith("closed"))
	if _, ran := guarded(t, "code_view"); ran {
		t.Fatal("the hook should refuse while the runtime lives")
	}
	closed = true
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ran := guarded(t, "code_view"); !ran {
		t.Fatal("a closed runtime left its hook behind")
	}
}

func TestStoppingAndRestartingThePluginLiftsAndRestoresTheHooks(t *testing.T) {
	rt := boot(t)
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	writeUserHooks(t, rt, userhooks.PreToolUse, "", refuseWith("closed"))
	if _, ran := guarded(t, "code_view"); ran {
		t.Fatal("the hook should refuse while the plugin runs")
	}

	if out := s.StopPlugin("userhooks"); out["success"] != true {
		t.Fatalf("stopping userhooks: %+v", out)
	}
	if _, ran := guarded(t, "code_view"); !ran {
		t.Error("a stopped plugin still refused a tool")
	}
	if st := s.GetHooksStatus(); st.Enabled || st.DisabledReason == "" {
		t.Errorf("the page must say the hooks are off: %+v", st)
	}

	if out := s.ReloadPlugin("userhooks"); out["success"] != true {
		t.Fatalf("restarting userhooks: %+v", out)
	}
	if _, ran := guarded(t, "code_view"); ran {
		t.Error("the restarted plugin does not refuse again")
	}
	if st := s.GetHooksStatus(); !st.Enabled || st.Total != 1 {
		t.Errorf("status after the restart: %+v", st)
	}
}

func TestClaudeToolNamesMapToOurs(t *testing.T) {
	for claude, want := range map[string]string{
		"Bash": "execute_bash", "Read": "code_view", "Write": "code_write", "Edit": "code_edit",
		"MultiEdit": "code_multiedit", "Glob": "code_glob", "Grep": "code_grep", "LS": "code_ls",
	} {
		if got := claudeToolAliases[claude]; len(got) != 1 || got[0] != want {
			t.Errorf("%s -> %v, want %s", claude, got, want)
		}
	}
}
