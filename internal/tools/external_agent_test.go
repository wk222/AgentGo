package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/acpclient"
)

// TestHelperACPAgent is not a test: with ACPTOOL_HELPER=1 the test binary plays a small ACP agent
// on stdio. A prompt containing "edit" makes it ask permission for an edit and report the answer;
// every prompt reports how many turns this process has handled.
func TestHelperACPAgent(t *testing.T) {
	if os.Getenv("ACPTOOL_HELPER") != "1" {
		t.Skip("only runs as a child process")
	}
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<24)
	turns := 0
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		switch m.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "mock"}, "authMethods": []any{}}})
		case "session/new":
			var p struct{ Cwd string }
			_ = json.Unmarshal(m.Params, &p)
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"sessionId": "s1"}})
			os.Setenv("ACPTOOL_CWD", p.Cwd)
		case "session/prompt":
			turns++
			var p struct {
				Prompt []struct{ Text string } `json:"prompt"`
			}
			_ = json.Unmarshal(m.Params, &p)
			text := p.Prompt[0].Text
			reply := fmt.Sprintf("turn %d in %s: %s", turns, filepath.Base(os.Getenv("ACPTOOL_CWD")), text)
			if strings.Contains(text, "edit") {
				send(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "session/request_permission", "params": map[string]any{
					"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "e1", "title": "Edit a.txt", "kind": "edit"},
					"options": []map[string]any{{"optionId": "ok", "name": "Allow", "kind": "allow_once"}, {"optionId": "nope", "name": "Reject", "kind": "reject_once"}},
				}})
				if !in.Scan() { // the very next line is the client's answer
					os.Exit(1)
				}
				var r struct {
					Result struct {
						Outcome struct {
							OptionID string `json:"optionId"`
						} `json:"outcome"`
					} `json:"result"`
				}
				_ = json.Unmarshal(in.Bytes(), &r)
				reply += " perm=" + r.Result.Outcome.OptionID
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s1",
				"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": reply}}}})
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"stopReason": "end_turn"}})
		}
	}
	os.Exit(0)
}

func externalAgentTool(t *testing.T) (*Registry, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := json.Marshal(map[string]string{
		"mock":  `"` + exe + `" -test.run=TestHelperACPAgent`,
		"ghost": "no-such-binary-xyz --acp",
	})
	t.Setenv("AGENTGO_EXTERNAL_AGENTS", string(custom))
	t.Setenv("ACPTOOL_HELPER", "1")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	if err := RegisterExternalAgentTool(r, root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(acpclient.Default.CloseAll)
	return r, root
}

func runExternal(t *testing.T, r *Registry, args string) externalAgentOutput {
	t.Helper()
	var out externalAgentOutput
	raw := invokeByName(t, r, context.Background(), "external_agent", args)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

func TestExternalAgentOneShotRunsInTheWorkspace(t *testing.T) {
	r, root := externalAgentTool(t)
	out := runExternal(t, r, `{"agent":"mock","prompt":"say hi"}`)
	if !out.Success || out.StopReason != "end_turn" || !strings.Contains(out.Text, "say hi") || !strings.Contains(out.Text, filepath.Base(root)) {
		t.Fatalf("%+v", out)
	}
	if got := runExternal(t, r, `{"agent":"mock","prompt":"again"}`); !strings.Contains(got.Text, "turn 1") {
		t.Fatalf("an unnamed run is a fresh agent each time: %q", got.Text)
	}
	if len(acpclient.Default.List()) != 0 {
		t.Fatal("a one-shot run must not leave a session behind")
	}
}

func TestExternalAgentPermissionIsReadOnlyUnlessRaised(t *testing.T) {
	r, _ := externalAgentTool(t)
	out := runExternal(t, r, `{"agent":"mock","prompt":"please edit a.txt"}`)
	if !strings.Contains(out.Text, "perm=nope") || len(out.Denied) != 1 || !strings.Contains(out.Message, "permission=read_only") {
		t.Fatalf("the default must refuse edits and say how to allow them: %+v", out)
	}
	out = runExternal(t, r, `{"agent":"mock","prompt":"please edit a.txt","permission":"edit"}`)
	if !strings.Contains(out.Text, "perm=ok") || len(out.Denied) != 0 {
		t.Fatalf("permission=edit must allow it: %+v", out)
	}
}

func TestExternalAgentSessionKeepsAgentAlive(t *testing.T) {
	r, _ := externalAgentTool(t)
	first := runExternal(t, r, `{"agent":"mock","prompt":"one","session":"review","cwd":"sub"}`)
	second := runExternal(t, r, `{"agent":"mock","prompt":"two","session":"review","cwd":"sub"}`)
	if !strings.Contains(first.Text, "turn 1 in sub") || !strings.Contains(second.Text, "turn 2 in sub") {
		t.Fatalf("a labelled session must carry on in the same agent:\n%q\n%q", first.Text, second.Text)
	}
	if list := runExternal(t, r, `{"action":"list"}`); len(list.Sessions) != 1 || list.Sessions[0].Label != "review" {
		t.Fatalf("list: %+v", list.Sessions)
	}
	if got := runExternal(t, r, `{"action":"close","session":"review"}`); !got.Success {
		t.Fatalf("close: %+v", got)
	}
	if got := runExternal(t, r, `{"action":"close","session":"review"}`); got.Success || got.Error == "" {
		t.Fatalf("closing twice must say there is nothing to close: %+v", got)
	}
}

func TestExternalAgentList(t *testing.T) {
	r, _ := externalAgentTool(t)
	out := runExternal(t, r, `{"action":"list"}`)
	byID := map[string]externalAgentInfo{}
	for _, a := range out.Agents {
		byID[a.ID] = a
	}
	for _, id := range []string{"claude", "codex", "gemini", "mock", "ghost"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("list misses %s: %+v", id, out.Agents)
		}
	}
	if !byID["mock"].Installed || byID["ghost"].Installed {
		t.Fatalf("installed flags: mock=%v ghost=%v", byID["mock"].Installed, byID["ghost"].Installed)
	}
}

func TestExternalAgentRefusals(t *testing.T) {
	r, root := externalAgentTool(t)
	outside := filepath.Dir(root)
	for name, args := range map[string]string{
		"no prompt":        `{"agent":"mock"}`,
		"no agent":         `{"prompt":"x"}`,
		"unknown agent":    `{"agent":"nope","prompt":"x"}`,
		"not installed":    `{"agent":"ghost","prompt":"x"}`,
		"bad permission":   `{"agent":"mock","prompt":"x","permission":"root"}`,
		"cwd escapes":      `{"agent":"mock","prompt":"x","cwd":"..\\.."}`,
		"cwd absolute out": fmt.Sprintf(`{"agent":"mock","prompt":"x","cwd":%q}`, outside),
		"cwd not a dir":    `{"agent":"mock","prompt":"x","cwd":"missing"}`,
		"unknown action":   `{"action":"fly"}`,
		"close no label":   `{"action":"close"}`,
	} {
		if out := runExternal(t, r, args); out.Success || out.Error == "" {
			t.Errorf("%s must be refused: %+v", name, out)
		}
	}
	if out := runExternal(t, r, `{"agent":"ghost","prompt":"x"}`); !strings.Contains(out.Error, "not installed") {
		t.Errorf("say why: %s", out.Error)
	}
}

func TestExternalAgentBadSettingIsReported(t *testing.T) {
	r, _ := externalAgentTool(t)
	t.Setenv("AGENTGO_EXTERNAL_AGENTS", "{broken")
	if out := runExternal(t, r, `{"action":"list"}`); out.Success || !strings.Contains(out.Error, "JSON") {
		t.Fatalf("a typo in the setting must not be silent: %+v", out)
	}
}

func TestExternalCwd(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := externalCwd(root, ""); err != nil || got != filepath.Clean(root) {
		t.Fatalf("default: %q %v", got, err)
	}
	if got, err := externalCwd(root, "a"); err != nil || got != filepath.Join(root, "a") {
		t.Fatalf("relative: %q %v", got, err)
	}
	if _, err := externalCwd(root, filepath.Join(root, "a", "..", "..")); err == nil {
		t.Fatal("a path that climbs out must be refused")
	}
	// A sibling that merely shares the prefix is not inside.
	sibling := root + "-other"
	_ = os.Mkdir(sibling, 0o755)
	t.Cleanup(func() { _ = os.Remove(sibling) })
	if _, err := externalCwd(root, sibling); err == nil {
		t.Fatal("a sibling folder sharing the prefix must be refused")
	}
}
