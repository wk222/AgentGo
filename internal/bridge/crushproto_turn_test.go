package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/crushproto"
	"agentgo/internal/engine"
)

// turnRunner replays a canned engine event sequence through crushTurn.
type turnRunner struct {
	script func(t *crushTurn, dir string)
	done   chan struct{}
}

func (r turnRunner) Run(_ context.Context, req crushproto.RunRequest, em *crushproto.Emitter) error {
	defer close(r.done)
	em.UserMessage(req.Prompt)
	t := newCrushTurn(em)
	r.script(t, em.WorkDir())
	t.finish()
	em.Complete(req, t.lastID, t.lastText, "", 0, 0)
	return nil
}

type apiMessage struct {
	Role  string            `json:"role"`
	Parts []json.RawMessage `json:"parts"`
}

func runTurn(t *testing.T, script func(*crushTurn, string)) []apiMessage {
	msgs, _ := runTurnFiles(t, script)
	return msgs
}

func runTurnFiles(t *testing.T, script func(*crushTurn, string)) ([]apiMessage, []crushproto.File) {
	t.Helper()
	r := turnRunner{script: script, done: make(chan struct{})}
	srv, err := crushproto.New(crushproto.Options{Runner: r, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { srv.Close(); ts.Close() })

	do := func(method, path string, body, out any) int {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	var ws map[string]any
	do("POST", "/v1/workspaces", map[string]any{"path": t.TempDir(), "data_dir": t.TempDir(), "client_id": "11111111-1111-4111-8111-111111111111", "env": []string{}}, &ws)
	wid, _ := ws["id"].(string)
	var sess crushproto.Session
	do("POST", "/v1/workspaces/"+wid+"/sessions", crushproto.Session{Title: "non-interactive"}, &sess)
	if code := do("POST", "/v1/workspaces/"+wid+"/agent", crushproto.RunRequest{SessionID: sess.ID, RunID: "r1", Prompt: "go"}, nil); code != http.StatusAccepted {
		t.Fatalf("POST agent = %d", code)
	}
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not finish")
	}
	var msgs []apiMessage
	do("GET", "/v1/workspaces/"+wid+"/sessions/"+sess.ID+"/messages", nil, &msgs)
	var files []crushproto.File
	do("GET", "/v1/workspaces/"+wid+"/sessions/"+sess.ID+"/history", nil, &files)
	return msgs, files
}

func roles(ms []apiMessage) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Role)
	}
	return strings.Join(out, ",")
}

func TestCrushTurnToolCallsBecomeToolMessages(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		c.token("先看文件。")
		c.toolCall("c1", "view", `{"file_path":"a.go"}`)
		c.toolResult("c1", "view", "package main", false)
		c.toolCall("c2", "edit", `{"file_path":"a.go"}`)
		c.toolResult("c2", "edit", "boom", true)
		c.token("完成")
	})
	// user, assistant(text+call c1), tool(c1), assistant(call c2), tool(c2), assistant(text)
	if got, want := roles(msgs), "user,assistant,tool,assistant,tool,assistant"; got != want {
		t.Fatalf("roles = %s, want %s", got, want)
	}
	last, _ := json.Marshal(msgs[len(msgs)-1])
	if !strings.Contains(string(last), "完成") {
		t.Fatalf("final assistant message lost text: %s", last)
	}
	errTool, _ := json.Marshal(msgs[4])
	if !strings.Contains(string(errTool), "boom") {
		t.Fatalf("tool error not forwarded: %s", errTool)
	}
}

func TestCrushTurnCoalescesTokens(t *testing.T) {
	var want strings.Builder
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		for i := 0; i < 300; i++ {
			c.token("x")
			want.WriteString("x")
		}
	})
	if got := roles(msgs); got != "user,assistant" {
		t.Fatalf("roles = %s", got)
	}
	b, _ := json.Marshal(msgs[1])
	if !strings.Contains(string(b), want.String()) {
		t.Fatalf("assistant text lost deltas: %s", b)
	}
}

func TestCrushTurnIgnoresEventsAfterFinish(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		c.token("a")
		c.finish()
		c.token("late")
		c.toolCall("c9", "view", "{}")
	})
	if got := roles(msgs); got != "user,assistant" {
		t.Fatalf("roles = %s", got)
	}
}

func TestCrushTurnMapsEngineEvents(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		ev := func(tp engine.EventType, p map[string]any) { c.onEvent(engine.Event{Type: tp, Payload: p}) }
		ev(engine.EventToolCall, map[string]any{"id": "e1", "name": "ls", "arguments": "{}"})
		ev(engine.EventToolResult, map[string]any{"id": "e1", "name": "ls", "output": "ok", "is_error": false})
		ev(engine.EventToken, map[string]any{"delta": "done"})
	})
	if got, want := roles(msgs), "user,assistant,tool,assistant"; got != want {
		t.Fatalf("roles = %s, want %s", got, want)
	}
}

// fakeEm records the two tool events; other Emitter methods are never called.
type fakeEm struct {
	engine.Emitter
	got *[]engine.Event
}

func (f fakeEm) ToolCall(id, name, args string) {
	*f.got = append(*f.got, engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"id": id, "name": name, "arguments": args}})
}

func (f fakeEm) ToolResult(id, name, output string, isErr bool) {
	*f.got = append(*f.got, engine.Event{Type: engine.EventToolResult, Payload: map[string]any{"id": id, "name": name, "output": output, "is_error": isErr}})
}

func TestToolEventsAdapterForwardsToEmitter(t *testing.T) {
	var got []engine.Event
	var obs agent.ToolObserver = toolEvents{fakeEm{got: &got}}
	obs.ToolStarted("c1", "view", `{"a":1}`)
	obs.ToolFinished("c1", "view", "out", true)
	if len(got) != 2 || got[0].Type != engine.EventToolCall || got[1].Type != engine.EventToolResult {
		t.Fatalf("events = %+v", got)
	}
	if got[0].Payload["id"] != "c1" || got[1].Payload["is_error"] != true || got[1].Payload["output"] != "out" {
		t.Fatalf("payloads = %+v / %+v", got[0].Payload, got[1].Payload)
	}
}

func TestEditToolsPopulateModifiedFiles(t *testing.T) {
	var path string
	_, files := runTurnFiles(t, func(c *crushTurn, dir string) {
		path = filepath.Join(dir, "a.go")
		if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		args := fmt.Sprintf(`{"file_path":%q}`, path)
		c.toolCall("e1", "code_edit", args)
		if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil { // the "tool" runs
			t.Fatal(err)
		}
		c.toolResult("e1", "code_edit", "ok", false)

		// A failed edit and a read-only tool must not record anything.
		c.toolCall("e2", "code_edit", args)
		c.toolResult("e2", "code_edit", "fail", true)
		c.toolCall("v1", "code_view", args)
		c.toolResult("v1", "code_view", "x", false)
	})
	if len(files) != 2 {
		t.Fatalf("want before+after versions, got %+v", files)
	}
	if files[0].Version != 0 || files[0].Content != "old\n" || files[1].Version != 1 || files[1].Content != "new\n" {
		t.Fatalf("versions = %+v", files)
	}
	if files[0].Path != path {
		t.Fatalf("path = %q, want %q", files[0].Path, path)
	}
}

func TestEngineFileChangeEventPopulatesHistory(t *testing.T) {
	_, files := runTurnFiles(t, func(c *crushTurn, _ string) {
		c.onEvent(engine.Event{Type: engine.EventFileChange, Payload: map[string]any{"path": "/x/b.txt", "before": "", "after": "hi"}})
		c.onEvent(engine.Event{Type: engine.EventFileChange, Payload: map[string]any{"path": "/x/b.txt", "before": "hi", "after": "hi2"}})
	})
	if len(files) != 3 || files[2].Content != "hi2" || files[2].Version != 2 {
		t.Fatalf("files = %+v", files)
	}
}

func TestSnapshotBeforeIgnoresOtherTools(t *testing.T) {
	if _, ok := snapshotBefore(t.TempDir(), "code_view", `{"file_path":"a.go"}`); ok {
		t.Fatal("read-only tool snapshotted")
	}
	if _, ok := snapshotBefore(t.TempDir(), "code_edit", `not json`); ok {
		t.Fatal("bad args snapshotted")
	}
	s, ok := snapshotBefore(t.TempDir(), "code_write", `{"file_path":"new.txt"}`)
	if !ok || !s.ok || s.before != "" {
		t.Fatalf("creation snapshot = %+v ok=%v", s, ok)
	}
}

func TestCrushTurnReasoningEvent(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		c.onEvent(engine.Event{Type: engine.EventReasoning, Payload: map[string]any{"delta": "Let me think."}})
		c.onEvent(engine.Event{Type: engine.EventReasoning, Payload: map[string]any{"delta": " The answer is 42."}})
		c.token("The answer is 42.")
	})
	if got := roles(msgs); got != "user,assistant" {
		t.Fatalf("roles = %s", got)
	}
	asst, _ := json.Marshal(msgs[1])
	asstStr := string(asst)
	if !strings.Contains(asstStr, `"reasoning"`) {
		t.Fatalf("missing reasoning part: %s", asstStr)
	}
	if !strings.Contains(asstStr, "Let me think. The answer is 42.") {
		t.Fatalf("missing reasoning text: %s", asstStr)
	}
	if !strings.Contains(asstStr, "The answer is 42.") {
		t.Fatalf("missing content text: %s", asstStr)
	}
	if !strings.Contains(asstStr, `"finished_at"`) {
		t.Fatalf("reasoning part missing finished_at: %s", asstStr)
	}
}

// unescaped renders a message the way a client sees it: encoding/json escapes
// '<' and '>' as \u003c/\u003e, which would hide leaked tags from substring checks.
func unescaped(m apiMessage) string {
	b, _ := json.Marshal(m)
	return strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(string(b))
}

// Tags may be split across stream chunks; they must neither leak into the
// visible text nor be lost, and a held-back partial tag must be flushed at the end.
func TestCrushTurnThinkTagsSplitAcrossChunks(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		for _, d := range []string{"<thi", "nk>pla", "n it</th", "ink>ans", "wer <", "b>"} {
			c.token(d)
		}
	})
	s := unescaped(msgs[1])
	if strings.Contains(s, "think>") || strings.Contains(s, "<thi") || strings.Contains(s, "</th") {
		t.Fatalf("tag leaked into message: %s", s)
	}
	if !strings.Contains(s, `"thinking":"plan it"`) {
		t.Fatalf("reasoning text wrong: %s", s)
	}
	if !strings.Contains(s, `"text":"answer <b>"`) {
		t.Fatalf("answer text wrong (partial '<' not flushed?): %s", s)
	}
}

func TestCrushTurnTrailingPartialTagIsFlushed(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		c.token("a < b and <")
	})
	if s := unescaped(msgs[1]); !strings.Contains(s, `"text":"a < b and <"`) {
		t.Fatalf("held-back tail lost: %s", s)
	}
}
func TestCrushTurnThinkTags(t *testing.T) {
	msgs := runTurn(t, func(c *crushTurn, _ string) {
		c.token("<think>I should check math</think>Here is the result.")
	})
	if got := roles(msgs); got != "user,assistant" {
		t.Fatalf("roles = %s", got)
	}
	asst, _ := json.Marshal(msgs[1])
	asstStr := string(asst)
	if !strings.Contains(asstStr, `"reasoning"`) {
		t.Fatalf("missing reasoning part from think tags: %s", asstStr)
	}
	if !strings.Contains(asstStr, "I should check math") {
		t.Fatalf("missing thinking text: %s", asstStr)
	}
	if !strings.Contains(asstStr, "Here is the result.") {
		t.Fatalf("missing content text: %s", asstStr)
	}
}
