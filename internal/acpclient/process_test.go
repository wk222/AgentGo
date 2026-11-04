package acpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperAgent is not a test: when ACPCLIENT_HELPER is set the test binary plays an ACP agent
// on stdio, so the process plumbing is exercised with a real child.
func TestHelperAgent(t *testing.T) {
	if os.Getenv("ACPCLIENT_HELPER") != "1" {
		t.Skip("only runs as a child process")
	}
	fmt.Println("hello — this banner is not JSON-RPC and must be ignored")
	a := newFake(t)
	a.out = os.Stdout
	a.onPrompt = func(a *fakeAgent, id json.RawMessage, text string) {
		wd, _ := os.Getwd()
		a.say("cwd=" + wd + " env=" + os.Getenv("ACPCLIENT_MARK") + " " + text)
		a.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	a.serve(os.Stdin) // returns when the client closes our stdin
	os.Exit(0)
}

func helperConfig(t *testing.T) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Name:    "helper",
		Command: `"` + exe + `" -test.run=TestHelperAgent`,
		Cwd:     t.TempDir(),
		Env:     []string{"ACPCLIENT_HELPER=1", "ACPCLIENT_MARK=marked"},
	}
}

func TestRealChildProcessRoundTrip(t *testing.T) {
	cfg := helperConfig(t)
	c, err := Launch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.Prompt(context.Background(), "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(turn.Text, "env=marked") || !strings.Contains(turn.Text, "ping") {
		t.Fatalf("the child must get the extra environment: %q", turn.Text)
	}
	if !strings.Contains(strings.ToLower(turn.Text), strings.ToLower(cfg.Cwd)) {
		t.Fatalf("the child must run in the working directory %q: %q", cfg.Cwd, turn.Text)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return: the child was not stopped")
	}
	if c.Alive() {
		t.Fatal("a closed session is not alive")
	}
	c.Close() // a second Close is harmless
}

func TestLaunchFailureShowsWhyTheCommandFailed(t *testing.T) {
	_, err := Launch(context.Background(), Config{Name: "ghost", Command: "definitely-not-an-agent-binary --acp", Cwd: t.TempDir(), InitTimeout: 20 * time.Second})
	if err == nil {
		t.Fatal("a command that does not exist cannot become an agent")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("the error should name the agent and say it ended: %v", err)
	}
}

func TestLaunchRejectsBadConfig(t *testing.T) {
	if _, err := Launch(context.Background(), Config{Command: "  "}); err == nil {
		t.Fatal("an empty command must be refused")
	}
	if _, err := Launch(context.Background(), Config{Command: "x", Cwd: t.TempDir() + "/missing"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing working directory must be reported: %v", err)
	}
}
