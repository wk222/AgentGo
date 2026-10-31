package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"agentgo/internal/userhooks"
)

func echoLine(text string) string {
	if runtime.GOOS == "windows" {
		return "echo " + text
	}
	return "echo '" + text + "'"
}

func TestAMessageRefusedByAHookNeverReachesTheModel(t *testing.T) {
	rt := boot(t)
	svc := &AppService{rt: rt}
	writeUserHooks(t, rt, userhooks.UserPromptSubmit, "", refuseWith("not today"))

	// sendMessageCore must answer before it touches the model: this runtime has no API key at all
	res := svc.sendMessageCore(context.Background(), "s1", "please deploy", nil, nil)
	if res.Error != "" || len(res.Messages) != 1 {
		t.Fatalf("result = %+v", res)
	}
	m := res.Messages[0]
	if m.Role != "assistant" || !strings.HasPrefix(m.Content, "[钩子拦截]") || !strings.Contains(m.Content, "not today") || !strings.Contains(m.Content, "UserPromptSubmit") {
		t.Errorf("the notice = %+v", m)
	}
	if runs := rt.hooksEngine().Status().Recent; len(runs) != 1 || runs[0].Result != "blocked" || runs[0].Event != "UserPromptSubmit" {
		t.Errorf("records = %+v", runs)
	}
}

func TestHookContextComesAfterTheExpansionOfCommandsAndMentions(t *testing.T) {
	rt := boot(t)
	svc := &AppService{rt: rt}
	if err := os.WriteFile(filepath.Join(rt.WorkspaceRoot(), "sample.txt"), []byte("Hello AgentGo Mentions!"), 0o644); err != nil {
		t.Fatal(err)
	}
	typed := "/review 重点看 @sample.txt"

	plain, refused := svc.preparePrompt(context.Background(), "s1", typed)
	if refused != nil || plain != svc.expandUserInput(context.Background(), typed) {
		t.Fatalf("without hooks the input must be exactly the expansion: refused=%v", refused)
	}

	writeUserHooks(t, rt, userhooks.UserPromptSubmit, "", echoLine("the code word is HERON"))
	got, refused := svc.preparePrompt(context.Background(), "s1", typed)
	if refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	file, title, word := strings.Index(got, "Hello AgentGo Mentions!"), strings.Index(got, hookContextTitle), strings.Index(got, "the code word is HERON")
	if file < 0 || title < file || word < title {
		t.Errorf("order is wrong (file %d, title %d, word %d):\n%s", file, title, word, got)
	}
	if !strings.HasPrefix(got, plain) {
		t.Errorf("the hook's text must be appended to the expanded input, not mixed into it")
	}
	if !strings.HasSuffix(got, "the code word is HERON") {
		t.Errorf("the input does not end with the hook's text:\n%s", got)
	}
}

func TestStopHookGetsTheLastReplyAndDoesNotBlock(t *testing.T) {
	rt := boot(t)
	seen := filepath.Join(t.TempDir(), "stop.json")
	writeUserHooks(t, rt, userhooks.Stop, "", copyStdinTo(seen))

	rt.fireStopHook("s7", "all done, tests pass")
	stopHooksWG.Wait()

	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the Stop hook did not run: %v (records %+v)", err, rt.hooksEngine().Status().Recent)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &got); err != nil {
		t.Fatalf("stdin = %q: %v", raw, err)
	}
	if got["hook_event_name"] != "Stop" || got["session_id"] != "s7" || got["last_assistant_message"] != "all done, tests pass" || got["cwd"] != rt.WorkspaceRoot() {
		t.Errorf("stdin = %s", raw)
	}
}

func TestStopHooksAreSkippedWhenTooManyAreStillRunning(t *testing.T) {
	rt := boot(t)
	seen := filepath.Join(t.TempDir(), "stop.json")
	writeUserHooks(t, rt, userhooks.Stop, "", copyStdinTo(seen))

	atomic.StoreInt32(&stopHooksInFlight, maxStopHooksInFlight)
	t.Cleanup(func() { atomic.StoreInt32(&stopHooksInFlight, 0) })
	rt.fireStopHook("s1", "x")
	stopHooksWG.Wait()
	if _, err := os.Stat(seen); err == nil {
		t.Error("a Stop hook ran although the limit was reached")
	}
	if got := atomic.LoadInt32(&stopHooksInFlight); got != maxStopHooksInFlight {
		t.Errorf("the counter drifted to %d", got)
	}
}

func TestNoStopHookNoGoroutine(t *testing.T) {
	rt := boot(t)
	before := atomic.LoadInt32(&stopHooksInFlight)
	rt.fireStopHook("s1", "x") // no hooks.json
	if after := atomic.LoadInt32(&stopHooksInFlight); after != before {
		t.Errorf("counter %d -> %d", before, after)
	}
}

func TestTurnEnd(t *testing.T) {
	text := ChatMessageDTO{Role: "assistant", Type: "text", Content: "the answer"}
	for _, tc := range []struct {
		name  string
		res   SendMessageResult
		reply string
		ended bool
	}{
		{"a finished answer", SendMessageResult{Messages: []ChatMessageDTO{text}}, "the answer", true},
		{"an answer without a type", SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Content: "plain"}}}, "plain", true},
		{"the last of several", SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: "first"}, text}}, "the answer", true},
		{"an error is not an end", SendMessageResult{Error: "boom"}, "", false},
		{"waiting for an approval", SendMessageResult{Messages: []ChatMessageDTO{text, {Role: "assistant", Type: "approval", Status: "pending"}}}, "", false},
		{"waiting for an answer", SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "question", Status: "pending"}}}, "", false},
		{"a run that used tools and said nothing", SendMessageResult{}, "", true},
	} {
		reply, ended := turnEnd(tc.res)
		if reply != tc.reply || ended != tc.ended {
			t.Errorf("%s: turnEnd = (%q, %v), want (%q, %v)", tc.name, reply, ended, tc.reply, tc.ended)
		}
	}
}

func TestHooksStatusForTheSettingsPage(t *testing.T) {
	rt := boot(t)
	svc := &AppService{rt: rt}

	st := svc.GetHooksStatus()
	if !st.Enabled || st.Exists || st.Total != 0 || st.Path != filepath.Join(rt.dataDir, userHooksFile) {
		t.Fatalf("before a file exists: %+v", st)
	}
	if st.Hooks == nil || st.Problems == nil || st.Recent == nil {
		t.Errorf("the page needs lists, not nulls: %+v", st)
	}

	writeUserHooks(t, rt, userhooks.PreToolUse, "Bash", "echo checked")
	st = svc.ReloadHooks()
	if !st.Exists || st.Total != 1 || len(st.Hooks) != 1 {
		t.Fatalf("after Reload: %+v", st)
	}
	if h := st.Hooks[0]; h.Event != "PreToolUse" || h.Matcher != "Bash" || h.Command != "echo checked" || h.TimeoutS != 20 || h.OnFailure != "continue" {
		t.Errorf("hook = %+v", h)
	}

	if err := os.WriteFile(filepath.Join(rt.dataDir, userHooksFile), []byte("{ broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	st = svc.ReloadHooks()
	if st.Error == "" || st.Total != 1 {
		t.Errorf("a broken edit must report the error and keep the old hooks: %+v", st)
	}
}

func TestHooksStatusWhenSwitchedOff(t *testing.T) {
	t.Setenv("AGENTGO_USER_HOOKS", "0")
	rt := boot(t)
	svc := &AppService{rt: rt}
	for _, st := range []userhooks.Status{svc.GetHooksStatus(), svc.ReloadHooks()} {
		if st.Enabled || !strings.Contains(st.DisabledReason, "AGENTGO_USER_HOOKS") || st.Path != filepath.Join(rt.dataDir, userHooksFile) {
			t.Errorf("status = %+v", st)
		}
		if st.Hooks == nil || st.Problems == nil || st.Recent == nil || st.Counts == nil {
			t.Errorf("the page needs lists, not nulls: %+v", st)
		}
	}
}
