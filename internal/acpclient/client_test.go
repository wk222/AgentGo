package acpclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPromptCollectsTheAnswerAndTheToolCalls(t *testing.T) {
	a := newFake(t)
	a.onPrompt = func(a *fakeAgent, id json.RawMessage, text string) {
		a.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read main.go", "kind": "read", "status": "pending"})
		a.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
		a.say("你好，")
		a.say("世界")
		a.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking…"}})
		a.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	c := launchFake(t, a, ReadOnly)
	if c.AgentName() != "fake-agent" || a.cwd != "/work" {
		t.Fatalf("name=%q cwd=%q", c.AgentName(), a.cwd)
	}
	var events []string
	turn, err := c.Prompt(context.Background(), "看看 main.go", func(e Event) { events = append(events, e.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "你好，世界" || turn.StopReason != "end_turn" {
		t.Fatalf("turn: %+v", turn)
	}
	if len(turn.Tools) != 1 || turn.Tools[0].Title != "Read main.go" || turn.Tools[0].Status != "completed" || turn.Tools[0].Kind != "read" {
		t.Fatalf("an update must fold into its call and keep the fields it leaves out: %+v", turn.Tools)
	}
	if got := strings.Join(events, ","); got != "tool,tool,text,text" {
		t.Fatalf("events: %s", got)
	}
	if a.prompts[0] != "看看 main.go" {
		t.Fatalf("the agent got %q", a.prompts[0])
	}
}

func TestSessionKeepsContextAcrossTurns(t *testing.T) {
	a := newFake(t)
	c := launchFake(t, a, ReadOnly)
	for _, q := range []string{"one", "two"} {
		turn, err := c.Prompt(context.Background(), q, nil)
		if err != nil || turn.Text != "ok" {
			t.Fatalf("%s: %+v %v", q, turn, err)
		}
	}
	if len(a.prompts) != 2 {
		t.Fatalf("prompts: %v", a.prompts)
	}
}

func TestPermissionLevelsDecideWhatTheAgentMayDo(t *testing.T) {
	cases := []struct {
		perm      Permission
		kind      string
		wantReply string
		denied    bool
	}{
		{ReadOnly, "read", "yes", false},
		{ReadOnly, "search", "yes", false},
		{ReadOnly, "edit", "no", true},
		{ReadOnly, "execute", "no", true},
		{ReadOnly, "other", "no", true},
		{Edit, "edit", "yes", false},
		{Edit, "delete", "yes", false},
		{Edit, "execute", "no", true},
		{Edit, "switch_mode", "no", true},
		{Full, "execute", "yes", false},
		{Full, "other", "yes", false},
	}
	for _, tc := range cases {
		a := newFake(t)
		a.onPrompt = func(a *fakeAgent, id json.RawMessage, _ string) {
			a.askPermission(tc.kind, "do "+tc.kind)
			a.say("done")
			a.reply(id, map[string]any{"stopReason": "end_turn"})
		}
		c := launchFake(t, a, tc.perm)
		turn, err := c.Prompt(context.Background(), "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.permOutcomes) != 1 || a.permOutcomes[0] != tc.wantReply {
			t.Errorf("%s/%s: the agent was answered %v, want %s", tc.perm, tc.kind, a.permOutcomes, tc.wantReply)
		}
		if (len(turn.Denied) > 0) != tc.denied {
			t.Errorf("%s/%s: denied=%v", tc.perm, tc.kind, turn.Denied)
		}
		if tc.denied && !strings.Contains(turn.Denied[0], "do "+tc.kind) {
			t.Errorf("a refusal must say what was refused: %v", turn.Denied)
		}
	}
}

func TestAgentWithoutRejectOptionIsCancelled(t *testing.T) {
	a := newFake(t)
	c := launchFake(t, a, ReadOnly)
	res, rpcErr := c.handleRequest("session/request_permission", json.RawMessage(
		`{"toolCall":{"toolCallId":"x","kind":"execute","title":"rm"},"options":[{"optionId":"y","name":"Allow","kind":"allow_once"}]}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	out := res.(map[string]any)["outcome"].(map[string]any)["outcome"]
	if out != "cancelled" {
		t.Fatalf("a forbidden call with nothing to reject with must be cancelled, got %v", out)
	}
}

func TestUnsupportedRequestsGetMethodNotFound(t *testing.T) {
	a := newFake(t)
	c := launchFake(t, a, Full)
	for _, m := range []string{"fs/read_text_file", "fs/write_text_file", "terminal/create"} {
		if _, err := c.handleRequest(m, nil); err == nil || err.Code != -32601 {
			t.Errorf("%s: %v", m, err)
		}
	}
}

func TestAuthenticationIsRetriedOnce(t *testing.T) {
	a := newFake(t)
	a.authRequired = true
	c := launchFake(t, a, ReadOnly)
	if !a.authed {
		t.Fatal("the client never signed in")
	}
	if turn, err := c.Prompt(context.Background(), "hi", nil); err != nil || turn.Text != "ok" {
		t.Fatalf("%+v %v", turn, err)
	}
}

func TestCancelAsksTheAgentToStopAndKeepsWhatItWrote(t *testing.T) {
	a := newFake(t)
	a.onPrompt = func(a *fakeAgent, id json.RawMessage, _ string) {
		a.say("half an ans")
		<-a.cancelled
		a.reply(id, map[string]any{"stopReason": "cancelled"})
	}
	c := launchFake(t, a, ReadOnly)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	turn, err := c.Prompt(ctx, "long task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.StopReason != "cancelled" || turn.Text != "half an ans" {
		t.Fatalf("turn: %+v", turn)
	}
	if !c.Alive() {
		t.Fatal("a polite cancel must not kill an agent that answered it")
	}
}

func TestCancelKillsAnAgentThatIgnoresIt(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 10 s grace period")
	}
	a := newFake(t)
	a.onPrompt = func(a *fakeAgent, id json.RawMessage, _ string) { <-a.exited }
	c := launchFake(t, a, ReadOnly)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	turn, err := c.Prompt(ctx, "hang", nil)
	if err != nil || turn.StopReason != "cancelled" {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	if time.Since(start) > 15*time.Second || c.Alive() {
		t.Fatalf("the agent that ignored cancel must be killed (alive=%v after %s)", c.Alive(), time.Since(start))
	}
}

func TestACrashedAgentIsExplainedByItsLog(t *testing.T) {
	a := newFake(t)
	a.onPrompt = func(a *fakeAgent, id json.RawMessage, _ string) {
		a.out.(interface{ Close() error }).Close() // the agent dies mid-turn
	}
	c := launchFake(t, a, ReadOnly)
	_, err := c.Prompt(context.Background(), "go", nil)
	if err == nil || !strings.Contains(err.Error(), "fake agent log: boom") || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("the error must say the agent ended and show its log: %v", err)
	}
	if c.Alive() {
		t.Fatal("a dead agent is not alive")
	}
	if _, err := c.Prompt(context.Background(), "again", nil); err == nil {
		t.Fatal("a dead session must refuse another prompt")
	}
}

func TestInitializeFailureIncludesTheAgentLog(t *testing.T) {
	a := newFake(t)
	tr := a.transport()
	_ = tr.out.(interface{ Close() error }).Close() // the agent never answers
	_, err := connect(context.Background(), Config{Name: "fake", InitTimeout: time.Second}, tr)
	if err == nil || !strings.Contains(err.Error(), "fake agent log") {
		t.Fatalf("got %v", err)
	}
}

func TestParsePermission(t *testing.T) {
	for in, want := range map[string]Permission{"": ReadOnly, "read-only": ReadOnly, "READ_ONLY": ReadOnly, "edit": Edit, "full": Full, "yolo": Full} {
		if got, err := ParsePermission(in); err != nil || got != want {
			t.Errorf("%q → %v, %v", in, got, err)
		}
	}
	if _, err := ParsePermission("root"); err == nil {
		t.Fatal("an unknown level must be refused, not guessed")
	}
}
