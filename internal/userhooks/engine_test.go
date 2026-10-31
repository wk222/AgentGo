package userhooks

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// TestHookHelperProcess is not a test: the tests below run this test binary as
// the hook script, so what a script does is the same on every platform. The
// first argument after "--" is what to do, the second (optional) a file to
// write to.
func TestHookHelperProcess(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	file := ""
	if len(args) > 1 {
		file = args[1]
	}
	appendTo := func(text string) {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
	}
	switch args[0] {
	case "ok":
	case "block2":
		fmt.Fprint(os.Stderr, "never delete the build folder\r\n")
		os.Exit(2)
	case "block2-silent":
		os.Exit(2)
	case "gbk-reason": // 公司规定, as a Chinese Windows prints it
		_, _ = os.Stderr.Write([]byte("\xb9\xab\xcb\xbe\xb9\xe6\xb6\xa8"))
		os.Exit(2)
	case "block2-huge": // 900 KB of three-byte characters: neither cap lands between characters
		fmt.Fprint(os.Stderr, strings.Repeat("世", 300_000))
		os.Exit(2)
	case "json-block":
		fmt.Print(`{"decision":"block","reason":"policy says no"}`)
	case "json-deny":
		fmt.Print(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"denied by policy"}}`)
	case "json-stop":
		fmt.Print(`{"continue":false,"stopReason":"stopped by the script"}`)
	case "json-allow":
		fmt.Print(`{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"echo changed"}}}`)
	case "json-broken":
		fmt.Print(`{"decision": `)
	case "json-context":
		fmt.Print(`{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"the code word is HERON"}}`)
	case "plain-context":
		fmt.Print("the code word is HERON\r\n")
	case "exit1":
		fmt.Fprint(os.Stderr, "boom: a dependency is missing")
		os.Exit(1)
	case "sleep":
		time.Sleep(time.Minute)
	case "record":
		wd, _ := os.Getwd()
		_ = os.WriteFile(file, stdin, 0o644)
		env := "PWD=" + wd + "\n"
		for _, k := range []string{"AGENTGO_PROJECT_DIR", "CLAUDE_PROJECT_DIR", "AGENTGO_HOOK_EVENT", "AGENTGO_SESSION_ID", "AGENTGO_IN_HOOK"} {
			env += k + "=" + os.Getenv(k) + "\n"
		}
		_ = os.WriteFile(file+".env", []byte(env), 0o644)
	case "append-a", "append-b", "append-c":
		appendTo(strings.TrimPrefix(args[0], "append-") + "\n")
	case "serial":
		appendTo("start\n")
		time.Sleep(400 * time.Millisecond)
		appendTo("end\n")
	}
	os.Exit(0)
}

// helperLine is the command line of a hook script that does what mode says.
func helperLine(mode string, args ...string) string {
	line := `"` + os.Args[0] + `" -test.run=TestHookHelperProcess -- ` + mode
	for _, a := range args {
		line += ` "` + a + `"`
	}
	return line
}

// spec is one hook of a test's hooks.json.
type spec struct {
	event     string
	matcher   string
	command   string
	timeout   float64
	onFailure string
}

func hooksJSON(specs ...spec) string {
	events := map[string][]any{}
	for _, s := range specs {
		hook := map[string]any{"type": "command", "command": s.command}
		if s.timeout > 0 {
			hook["timeout"] = s.timeout
		}
		if s.onFailure != "" {
			hook["onFailure"] = s.onFailure
		}
		events[s.event] = append(events[s.event], map[string]any{"matcher": s.matcher, "hooks": []any{hook}})
	}
	b, _ := json.Marshal(map[string]any{"hooks": events})
	return string(b)
}

// newEngine writes the hooks.json and returns an engine that looks at it before every use.
func newEngine(t *testing.T, specs ...spec) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if len(specs) > 0 {
		if err := os.WriteFile(path, []byte(hooksJSON(specs...)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{Path: path, Aliases: map[string][]string{"Bash": {"execute_bash"}, "Read": {"code_view"}}}), dir
}

func preTool(dir, tool, input string) Event {
	return Event{Name: PreToolUse, SessionID: "s1", Cwd: dir, ToolName: tool, ToolInput: input, ToolUseID: "call_1"}
}

func TestExit2RefusesWithTheScriptsReason(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, matcher: "Bash", command: helperLine("block2")})
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{"command":"rm -rf build"}`))
	if !out.Blocked || out.Reason != "never delete the build folder" {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.HasPrefix(out.By, "PreToolUse[Bash] ") {
		t.Errorf("By = %q", out.By)
	}
	runs := e.Status().Recent
	if len(runs) != 1 || runs[0].Result != "blocked" || runs[0].ExitCode != 2 || runs[0].Tool != "execute_bash" || runs[0].Reason != out.Reason {
		t.Errorf("records = %+v", runs)
	}
}

func TestExit2WithoutAReasonStillSaysWhy(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("block2-silent")})
	out := e.Fire(context.Background(), preTool(dir, "code_view", `{}`))
	if !out.Blocked || !strings.Contains(out.Reason, "without a reason") {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestWhatAScriptPrintsAsJSON(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		blocked     bool
		reason      string
		warningPart string
	}{
		{"json-block", true, "policy says no", ""},
		{"json-deny", true, "denied by policy", ""},
		{"json-stop", true, "stopped by the script", ""},
		{"json-allow", false, "", `permissionDecision "allow" is not supported yet`},
		{"json-allow", false, "", "updatedInput is not supported yet"},
		{"json-broken", false, "", "cannot be read"},
		{"ok", false, "", ""},
	} {
		e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine(tc.mode)})
		out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
		if out.Blocked != tc.blocked || out.Reason != tc.reason {
			t.Errorf("%s: outcome = %+v", tc.mode, out)
		}
		if tc.warningPart != "" && !strings.Contains(strings.Join(out.Warnings, "\n"), tc.warningPart) {
			t.Errorf("%s: warnings = %v, want one with %q", tc.mode, out.Warnings, tc.warningPart)
		}
		if tc.warningPart == "" && len(out.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", tc.mode, out.Warnings)
		}
	}
}

func TestAScriptThatFailsLetsThingsThroughUnlessTheHookIsStrict(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("exit1")})
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if out.Blocked {
		t.Fatalf("a failing script must not stop the agent by default: %+v", out)
	}
	if w := strings.Join(out.Warnings, "\n"); !strings.Contains(w, "exit status 1") || !strings.Contains(w, "a dependency is missing") || !strings.Contains(w, "let through") {
		t.Errorf("warnings = %v", out.Warnings)
	}
	if runs := e.Status().Recent; len(runs) != 1 || runs[0].Result != "failed" || runs[0].ExitCode != 1 {
		t.Errorf("records = %+v", runs)
	}

	strict, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("exit1"), onFailure: "block"})
	out = strict.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if !out.Blocked || !strings.Contains(out.Reason, "exit status 1") || !strings.Contains(out.Reason, "set to block when it fails") {
		t.Fatalf("a strict hook must refuse when its script fails: %+v", out)
	}
}

func TestACommandThatDoesNotExistIsAFailureOfTheScript(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: "no-such-program-for-userhooks-test --flag"})
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if out.Blocked || len(out.Warnings) != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if runs := e.Status().Recent; len(runs) != 1 || runs[0].Result != "failed" {
		t.Errorf("records = %+v", runs)
	}
}

func TestATimedOutScriptIsEndedAndReported(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("sleep"), timeout: 1})
	start := time.Now()
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the script was not ended at its time limit: %v", took)
	}
	if out.Blocked || !strings.Contains(strings.Join(out.Warnings, "\n"), "timed out after 1 seconds") {
		t.Fatalf("outcome = %+v", out)
	}
	if runs := e.Status().Recent; len(runs) != 1 || runs[0].Result != "timeout" || runs[0].ExitCode != -1 {
		t.Errorf("records = %+v", runs)
	}

	strict, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("sleep"), timeout: 1, onFailure: "block"})
	if out := strict.Fire(context.Background(), preTool(dir, "execute_bash", `{}`)); !out.Blocked || !strings.Contains(out.Reason, "timed out") {
		t.Fatalf("a strict hook must refuse when its script times out: %+v", out)
	}
}

func TestCancellingTheRunEndsTheScriptAndSkipsTheRest(t *testing.T) {
	e, dir := newEngine(t,
		spec{event: PreToolUse, command: helperLine("sleep"), timeout: 30},
		spec{event: PreToolUse, command: helperLine("block2")})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(700 * time.Millisecond); cancel() }()
	start := time.Now()
	out := e.Fire(ctx, preTool(dir, "execute_bash", `{}`))
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("cancelling did not end the script: %v", took)
	}
	if out.Blocked || len(out.Warnings) != 0 {
		t.Errorf("a cancelled run is neither a refusal nor a failure: %+v", out)
	}
	runs := e.Status().Recent
	if len(runs) != 1 || runs[0].Result != "canceled" {
		t.Errorf("the second hook must not run after cancellation: %+v", runs)
	}
}

func TestPostToolUseAndStopCannotRefuse(t *testing.T) {
	for _, event := range []string{PostToolUse, Stop} {
		e, dir := newEngine(t,
			spec{event: event, command: helperLine("block2")},
			spec{event: event, command: helperLine("json-block")})
		out := e.Fire(context.Background(), Event{Name: event, SessionID: "s1", Cwd: dir, ToolName: "execute_bash"})
		if out.Blocked {
			t.Errorf("%s: %+v", event, out)
		}
		if w := strings.Join(out.Warnings, "\n"); !strings.Contains(w, "recorded only") {
			t.Errorf("%s: warnings = %v", event, out.Warnings)
		}
		for _, r := range e.Status().Recent {
			if r.Result != "ok" {
				t.Errorf("%s: record %+v", event, r)
			}
		}
	}
}

func TestUserPromptSubmitRefusesOrAddsContext(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		blocked bool
		reason  string
		context string
	}{
		{"block2", true, "never delete the build folder", ""},
		{"json-block", true, "policy says no", ""},
		{"json-context", false, "", "the code word is HERON"},
		{"plain-context", false, "", "the code word is HERON"},
		{"ok", false, "", ""},
	} {
		e, dir := newEngine(t, spec{event: UserPromptSubmit, matcher: "ignored", command: helperLine(tc.mode)})
		out := e.Fire(context.Background(), Event{Name: UserPromptSubmit, SessionID: "s1", Cwd: dir, Prompt: "hello"})
		if out.Blocked != tc.blocked || out.Reason != tc.reason || out.Context != tc.context {
			t.Errorf("%s: outcome = %+v", tc.mode, out)
		}
	}
}

func TestTheScriptGetsTheEventAsJSONAndTheEnvironment(t *testing.T) {
	record := filepath.Join(t.TempDir(), "seen.json")
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("record", record)})
	e.Fire(context.Background(), preTool(dir, "execute_bash", `{"command":"echo hi","timeout":5}`))

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the script did not run: %v (records %+v)", err, e.Status().Recent)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("stdin is not JSON: %v: %s", err, raw)
	}
	input, _ := got["tool_input"].(map[string]any)
	if got["hook_event_name"] != "PreToolUse" || got["tool_name"] != "execute_bash" || got["session_id"] != "s1" ||
		got["cwd"] != dir || got["tool_use_id"] != "call_1" || input["command"] != "echo hi" || input["timeout"] != float64(5) {
		t.Errorf("stdin = %s", raw)
	}
	if v, present := got["transcript_path"]; !present || v != nil {
		t.Errorf("transcript_path must be present and null: %s", raw)
	}
	if _, has := got["tool_response"]; has {
		t.Errorf("a PreToolUse event has no tool_response: %s", raw)
	}

	envText, err := os.ReadFile(record + ".env")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"AGENTGO_PROJECT_DIR=" + dir, "CLAUDE_PROJECT_DIR=" + dir,
		"AGENTGO_HOOK_EVENT=PreToolUse", "AGENTGO_SESSION_ID=s1", "AGENTGO_IN_HOOK=1",
	} {
		if !strings.Contains(string(envText), want+"\n") {
			t.Errorf("the script's environment lacks %q:\n%s", want, envText)
		}
	}
	// the script runs in the workspace
	wd := ""
	for _, line := range strings.Split(string(envText), "\n") {
		if strings.HasPrefix(line, "PWD=") {
			wd = strings.TrimPrefix(line, "PWD=")
		}
	}
	if a, _ := filepath.EvalSymlinks(wd); !strings.EqualFold(a, mustEval(t, dir)) {
		t.Errorf("working directory = %q, want %q", wd, dir)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPostToolUseGetsTheToolsResult(t *testing.T) {
	record := filepath.Join(t.TempDir(), "seen.json")
	e, dir := newEngine(t, spec{event: PostToolUse, matcher: "code_.*", command: helperLine("record", record)})
	e.Fire(context.Background(), Event{Name: PostToolUse, SessionID: "s1", Cwd: dir, ToolName: "code_view",
		ToolInput: `{"path":"a.go"}`, ToolOutput: "package a", ToolError: "partial read"})
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["hook_event_name"] != "PostToolUse" || got["tool_response"] != "package a" || got["tool_error"] != "partial read" {
		t.Errorf("stdin = %s", raw)
	}
}

func TestUserPromptSubmitAndStopPayloads(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		ev  Event
		key string
		val string
	}{
		{Event{Name: UserPromptSubmit, Prompt: "fix the bug"}, "prompt", "fix the bug"},
		{Event{Name: Stop, LastReply: "done, tests pass"}, "last_assistant_message", "done, tests pass"},
	} {
		record := filepath.Join(dir, tc.ev.Name+".json")
		e, _ := newEngine(t, spec{event: tc.ev.Name, command: helperLine("record", record)})
		tc.ev.SessionID, tc.ev.Cwd = "s9", dir
		e.Fire(context.Background(), tc.ev)
		raw, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("%s: %v", tc.ev.Name, err)
		}
		var got map[string]any
		_ = json.Unmarshal(raw, &got)
		if got[tc.key] != tc.val || got["hook_event_name"] != tc.ev.Name || got["session_id"] != "s9" {
			t.Errorf("%s: stdin = %s", tc.ev.Name, raw)
		}
		if _, has := got["tool_name"]; has {
			t.Errorf("%s: a tool field leaked in: %s", tc.ev.Name, raw)
		}
	}
}

func TestMatchersSeeBothOurToolNamesAndTheirs(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, matcher: "Bash", command: helperLine("block2")})
	if out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`)); !out.Blocked {
		t.Error("Claude's name for the shell tool must select execute_bash")
	}
	if out := e.Fire(context.Background(), preTool(dir, "code_view", `{}`)); out.Blocked {
		t.Error("a hook for Bash must not run for another tool")
	}
	if runs := e.Status().Recent; len(runs) != 1 {
		t.Errorf("a hook that does not apply must not start a process: %+v", runs)
	}
	if out := e.Fire(context.Background(), preTool(dir, "Bash", `{}`)); !out.Blocked {
		t.Error("a tool's own name must match as well")
	}
}

func TestHooksRunInTheOrderOfTheFileAndStopAtTheFirstRefusal(t *testing.T) {
	trail := filepath.Join(t.TempDir(), "trail.txt")
	e, dir := newEngine(t,
		spec{event: PreToolUse, command: helperLine("append-a", trail)},
		spec{event: PreToolUse, command: helperLine("block2")},
		spec{event: PreToolUse, command: helperLine("append-c", trail)})
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if !out.Blocked {
		t.Fatalf("outcome = %+v", out)
	}
	raw, _ := os.ReadFile(trail)
	if got := strings.Fields(string(raw)); len(got) != 1 || got[0] != "a" {
		t.Errorf("hooks that ran = %v; the one after the refusal must not run", got)
	}

	ok, dir := newEngine(t,
		spec{event: PreToolUse, command: helperLine("append-a", trail+"2")},
		spec{event: PreToolUse, command: helperLine("append-b", trail+"2")},
		spec{event: PreToolUse, command: helperLine("append-c", trail+"2")})
	ok.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	raw, _ = os.ReadFile(trail + "2")
	if got := strings.Join(strings.Fields(string(raw)), ""); got != "abc" {
		t.Errorf("order = %q, want abc", got)
	}
}

func TestContextOfSeveralHooksIsJoined(t *testing.T) {
	e, dir := newEngine(t,
		spec{event: UserPromptSubmit, command: helperLine("json-context")},
		spec{event: UserPromptSubmit, command: helperLine("plain-context")})
	out := e.Fire(context.Background(), Event{Name: UserPromptSubmit, Cwd: dir, Prompt: "x"})
	if out.Context != "the code word is HERON\nthe code word is HERON" {
		t.Errorf("context = %q", out.Context)
	}
}

func TestTheSameHookNeverRunsTwiceAtOnce(t *testing.T) {
	trail := filepath.Join(t.TempDir(), "trail.txt")
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("serial", trail), timeout: 30})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
		}()
	}
	wg.Wait()
	raw, _ := os.ReadFile(trail)
	if got := strings.Join(strings.Fields(string(raw)), " "); got != "start end start end" {
		t.Errorf("runs overlapped: %q", got)
	}
}

func TestEditingTheFileTakesEffectAndABrokenEditKeepsTheLastGoodOne(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("block2")})
	path := filepath.Join(dir, "hooks.json")
	fire := func() Outcome { return e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`)) }
	bump := func() { // some file systems keep the time of a quick second write unchanged
		later := time.Now().Add(time.Hour)
		_ = os.Chtimes(path, later, later)
	}

	if !fire().Blocked {
		t.Fatal("the first file refuses")
	}
	if err := os.WriteFile(path, []byte(`{ this is not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	bump()
	if !fire().Blocked {
		t.Error("a file that does not parse must not take the working hooks away")
	}
	if st := e.Status(); st.Error == "" || !strings.Contains(st.Error, "not valid JSON") || st.Total != 1 {
		t.Errorf("status after a broken edit = %+v", st)
	}

	if err := os.WriteFile(path, []byte(hooksJSON(spec{event: PreToolUse, command: helperLine("ok")})), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	_ = os.Chtimes(path, later, later)
	if fire().Blocked {
		t.Error("the fixed file no longer refuses")
	}
	if st := e.Status(); st.Error != "" || st.Total != 1 || st.SHA256 == "" || st.LoadedAt == "" {
		t.Errorf("status after the fix = %+v", st)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	before := len(e.Status().Recent)
	if out := fire(); out.Blocked || len(out.Warnings) != 0 {
		t.Errorf("without the file there are no hooks: %+v", out)
	}
	if st := e.Status(); st.Exists || st.Total != 0 || len(st.Recent) != before {
		t.Errorf("status after removing the file = %+v", st)
	}
}

func TestReloadReadsTheFileNow(t *testing.T) {
	e, dir := newEngine(t)
	e = New(Options{Path: filepath.Join(dir, "hooks.json"), CheckEvery: time.Hour})
	if st := e.Status(); st.Exists || st.Total != 0 {
		t.Fatalf("status = %+v", st)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(hooksJSON(spec{event: Stop, command: helperLine("ok")})), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(); st.Total != 0 {
		t.Errorf("a long CheckEvery should not have looked yet: %+v", st)
	}
	if st := e.Reload(); !st.Exists || st.Total != 1 || st.Counts[Stop] != 1 {
		t.Errorf("Reload = %+v", st)
	}
}

func TestWithoutAFileNothingRuns(t *testing.T) {
	e, dir := newEngine(t)
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if out.Blocked || out.Reason != "" || out.Context != "" || len(out.Warnings) != 0 {
		t.Errorf("outcome = %+v", out)
	}
	st := e.Status()
	if st.Exists || !st.Enabled || len(st.Recent) != 0 || st.Total != 0 || st.Path == "" {
		t.Errorf("status = %+v", st)
	}
	if st.Problems == nil || st.Recent == nil || st.Counts == nil {
		t.Errorf("a screen needs lists, not nulls: %+v", st)
	}
}

func TestANilEngineDoesNothing(t *testing.T) {
	var e *Engine
	if out := e.Fire(context.Background(), Event{Name: Stop}); out.Blocked {
		t.Error("nil engine refused")
	}
	if st := e.Reload(); st.Total != 0 || st.Problems == nil {
		t.Errorf("status = %+v", st)
	}
}

func TestAHookCannotStartItselfAgain(t *testing.T) {
	t.Setenv(EnvInHook, "1")
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("block2")})
	if out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`)); out.Blocked {
		t.Error("an agent started by a hook must not run hooks")
	}
	if st := e.Status(); st.Enabled || st.DisabledReason == "" || len(st.Recent) != 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestRunRecordsAreBoundedAndNewestFirst(t *testing.T) {
	e, _ := newEngine(t)
	for i := 1; i <= MaxRuns+50; i++ {
		e.record(Run{Event: Stop, Hook: fmt.Sprintf("h%d", i), Result: "ok"})
	}
	recent := e.Status().Recent
	if len(recent) != MaxRuns {
		t.Fatalf("kept %d records", len(recent))
	}
	if recent[0].Hook != fmt.Sprintf("h%d", MaxRuns+50) || recent[0].Seq != int64(MaxRuns+50) || recent[len(recent)-1].Hook != "h51" {
		t.Errorf("newest = %+v, oldest = %+v", recent[0], recent[len(recent)-1])
	}
}

func TestHugeOutputIsCappedAndTheReasonIsClipped(t *testing.T) {
	e, dir := newEngine(t, spec{event: PreToolUse, command: helperLine("block2-huge")})
	out := e.Fire(context.Background(), preTool(dir, "execute_bash", `{}`))
	if !out.Blocked {
		t.Fatalf("outcome = %+v", out)
	}
	if len(out.Reason) > maxReasonChars+len("…") || !strings.HasSuffix(out.Reason, "…") {
		t.Errorf("reason is %d bytes", len(out.Reason))
	}
	if !strings.HasPrefix(out.Reason, "世") || !utf8.ValidString(out.Reason) || strings.ContainsRune(out.Reason, '\uFFFD') {
		t.Errorf("clipping broke a character: %q…", out.Reason[:20])
	}
	if r := e.Status().Recent[0]; len(r.Reason) > maxRecordChars+len("…") {
		t.Errorf("record reason is %d bytes", len(r.Reason))
	}
}

func TestACommandPathWithSpacesRuns(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("copying the test binary is not supported here")
	}
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "my hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "hook helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	if err := os.WriteFile(exe, src, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `"` + exe + `" -test.run=TestHookHelperProcess -- block2`
	e, work := newEngine(t, spec{event: PreToolUse, command: line})
	if out := e.Fire(context.Background(), preTool(work, "execute_bash", `{}`)); !out.Blocked {
		t.Errorf("outcome = %+v (records %+v)", out, e.Status().Recent)
	}
}

func TestStatusListsTheConfiguredHooksInEventOrder(t *testing.T) {
	e, dir := newEngine(t)
	text := `{"hooks": {
	  "Stop": [{"hooks": [{"command": "notify.cmd", "timeout": 3}]}],
	  "PreToolUse": [{"matcher": "Bash|Edit", "hooks": [{"command": "guard.cmd", "timeout": 2.6, "onFailure": "block"}, {"command": "second.cmd"}]}],
	  "UserPromptSubmit": [{"hooks": [{"command": "ctx.cmd"}]}]
	}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	want := []HookInfo{
		{Event: PreToolUse, Matcher: "Bash|Edit", Command: "guard.cmd", TimeoutS: 3, OnFailure: OnFailureBlock},
		{Event: PreToolUse, Matcher: "Bash|Edit", Command: "second.cmd", TimeoutS: 30, OnFailure: OnFailureContinue},
		{Event: UserPromptSubmit, Command: "ctx.cmd", TimeoutS: 30, OnFailure: OnFailureContinue},
		{Event: Stop, Command: "notify.cmd", TimeoutS: 3, OnFailure: OnFailureContinue},
	}
	if len(st.Hooks) != len(want) {
		t.Fatalf("hooks = %+v", st.Hooks)
	}
	for i := range want {
		if st.Hooks[i] != want[i] {
			t.Errorf("hook %d = %+v, want %+v", i, st.Hooks[i], want[i])
		}
	}
}

func TestHasSaysWhetherAnEventHasHooks(t *testing.T) {
	e, _ := newEngine(t, spec{event: Stop, command: helperLine("ok")})
	if !e.Has(Stop) || e.Has(PreToolUse) || e.Has(UserPromptSubmit) {
		t.Error("Has must be true only for events with hooks")
	}
	var none *Engine
	if none.Has(Stop) {
		t.Error("a nil engine has no hooks")
	}
	t.Setenv(EnvInHook, "1")
	guarded, _ := newEngine(t, spec{event: Stop, command: helperLine("ok")})
	if guarded.Has(Stop) {
		t.Error("an engine inside a hook has no hooks")
	}
}

func TestStatusCountsAndProblems(t *testing.T) {
	e, dir := newEngine(t)
	text := `{"hooks": {"Stop": [{"hooks": [{"command": "a"}]}], "PreToolUse": [{"matcher": "(", "hooks": [{"command": "b"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	if !st.Exists || st.Total != 1 || st.Counts[Stop] != 1 || st.Counts[PreToolUse] != 0 || len(st.Problems) != 1 {
		t.Errorf("status = %+v", st)
	}
	if st.Problems[0].Where != "hooks.PreToolUse[0].matcher" {
		t.Errorf("problem = %+v", st.Problems[0])
	}
}
