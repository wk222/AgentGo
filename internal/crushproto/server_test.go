package crushproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sseEvent struct {
	Kind, Op string
	Payload  json.RawMessage
}

type harness struct {
	t    *testing.T
	srv  *Server
	ts   *httptest.Server
	ws   string
	path string // project directory registered for ws
	ev   chan sseEvent
	all  []sseEvent // every event consumed so far, in order
}

func newHarness(t *testing.T, runner Runner, mods ...func(*Options)) *harness {
	t.Helper()
	opts := Options{Runner: runner, Logf: func(string, ...any) {}}
	for _, m := range mods {
		m(&opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { srv.Close(); ts.Close() })
	h := &harness{t: t, srv: srv, ts: ts, ev: make(chan sseEvent, 1024), path: t.TempDir()}

	var wsResp map[string]any
	h.do("POST", "/v1/workspaces", map[string]any{"path": h.path, "data_dir": t.TempDir(), "client_id": newID(), "env": []string{}}, &wsResp)
	h.ws, _ = wsResp["id"].(string)
	if h.ws == "" {
		t.Fatalf("no workspace id: %v", wsResp)
	}
	h.subscribe()
	return h
}

func (h *harness) do(method, path string, body, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (h *harness) subscribe() {
	resp, err := http.Get(h.ts.URL + "/v1/workspaces/" + h.ws + "/events")
	if err != nil {
		h.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		h.t.Fatalf("events content-type = %q", ct)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var env struct {
				Type    string `json:"type"`
				Payload struct {
					Type    string          `json:"type"`
					Payload json.RawMessage `json:"payload"`
				} `json:"payload"`
			}
			if json.Unmarshal([]byte(line[6:]), &env) == nil {
				h.ev <- sseEvent{env.Type, env.Payload.Type, env.Payload.Payload}
			}
		}
	}()
}

// until collects events until pred matches one (returned) or the timeout hits.
func (h *harness) until(pred func(sseEvent) bool) (sseEvent, []sseEvent) {
	h.t.Helper()
	var seen []sseEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-h.ev:
			seen = append(seen, e)
			h.all = append(h.all, e)
			if pred(e) {
				return e, seen
			}
		case <-timeout:
			h.t.Fatalf("timed out; saw %d events: %v", len(seen), kinds(seen))
		}
	}
}

func kinds(es []sseEvent) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Kind + "/" + e.Op
	}
	return out
}

func (h *harness) startRun(prompt string) (sessionID string) {
	h.t.Helper()
	var sess Session
	h.do("POST", "/v1/workspaces/"+h.ws+"/sessions", Session{Title: "non-interactive"}, &sess) // the title `crush run` really sends
	code := h.do("POST", "/v1/workspaces/"+h.ws+"/agent", RunRequest{SessionID: sess.ID, RunID: "run-1", Prompt: prompt}, nil)
	if code != http.StatusAccepted {
		h.t.Fatalf("POST agent = %d, want 202", code)
	}
	return sess.ID
}

func (h *harness) answerPermission(action string) PermissionRequest {
	h.t.Helper()
	e, _ := h.until(func(e sseEvent) bool { return e.Kind == "permission_request" })
	var p PermissionRequest
	_ = json.Unmarshal(e.Payload, &p)
	var res map[string]bool
	h.do("POST", "/v1/workspaces/"+h.ws+"/permissions/grant", map[string]any{"action": action, "permission": p}, &res)
	if !res["resolved"] {
		h.t.Fatalf("grant not resolved: %v", res)
	}
	return p
}

func TestRunApprovedFlowMatchesRecordedShape(t *testing.T) {
	h := newHarness(t, ScriptRunner{Pace: time.Millisecond})
	sid := h.startRun("create hello")
	perm := h.answerPermission("allow")
	if perm.ToolName != "write" || perm.SessionID != sid {
		t.Fatalf("unexpected permission request: %+v", perm)
	}

	done, _ := h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })
	var rc map[string]string
	_ = json.Unmarshal(done.Payload, &rc)
	if rc["run_id"] != "run-1" || rc["session_id"] != sid || !strings.Contains(rc["text"], "approved") {
		t.Fatalf("run_complete payload: %v", rc)
	}

	// Ordering the real client relies on: user message first, tool result after the grant.
	order := kinds(h.all)
	idx := func(kind, op string) int {
		for i, s := range order {
			if s == kind+"/"+op {
				return i
			}
		}
		return -1
	}
	firstMsg := idx("message", "created")
	if firstMsg < 0 {
		t.Fatalf("no message/created: %v", order)
	}
	var first Message
	_ = json.Unmarshal(h.all[firstMsg].Payload, &first)
	if first.Role != "user" {
		t.Fatalf("first message event must be the user message, got role %q", first.Role)
	}
	if !(idx("permission_request", "created") < idx("run_complete", "updated")) {
		t.Fatalf("bad ordering: %v", order)
	}
	if idx("agent_event", "created") < 0 {
		t.Fatalf("missing agent_finished: %v", order)
	}

	// Stored state: user, assistant(tool_use), tool, assistant(text) = 4 messages.
	var msgs []Message
	h.do("GET", "/v1/workspaces/"+h.ws+"/sessions/"+sid+"/messages", nil, &msgs)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,tool,assistant" {
		t.Fatalf("roles = %v", roles)
	}
	last := msgs[3].Parts
	if last[len(last)-1].Type != "finish" || last[len(last)-1].Data["reason"] != "end_turn" {
		t.Fatalf("final message must end with finish/end_turn: %+v", last)
	}
	var sess Session
	h.do("GET", "/v1/workspaces/"+h.ws+"/sessions/"+sid, nil, &sess)
	if sess.MessageCount != 4 || sess.Title != "create hello" {
		t.Fatalf("session = %+v", sess)
	}
}

func TestRunDeniedFlow(t *testing.T) {
	h := newHarness(t, ScriptRunner{Pace: time.Millisecond})
	sid := h.startRun("x")
	h.answerPermission("deny")
	done, _ := h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })
	if !strings.Contains(string(done.Payload), "denied") {
		t.Fatalf("expected denial in final text: %s", done.Payload)
	}
	var msgs []Message
	h.do("GET", "/v1/workspaces/"+h.ws+"/sessions/"+sid+"/messages", nil, &msgs)
	if tr := msgs[2].Parts[0]; tr.Type != "tool_result" || tr.Data["is_error"] != true {
		t.Fatalf("denied tool result must be is_error: %+v", tr)
	}
}

func TestCancelWhileWaitingForPermission(t *testing.T) {
	h := newHarness(t, ScriptRunner{Pace: time.Millisecond})
	sid := h.startRun("x")
	h.until(func(e sseEvent) bool { return e.Kind == "permission_request" })

	var busy Session
	h.do("GET", "/v1/workspaces/"+h.ws+"/agent/sessions/"+sid, nil, &busy)
	if !busy.IsBusy {
		t.Fatal("session should be busy while waiting for permission")
	}
	h.do("POST", "/v1/workspaces/"+h.ws+"/agent/sessions/"+sid+"/cancel", nil, nil)

	h.until(func(e sseEvent) bool {
		var s Session
		return e.Kind == "session" && json.Unmarshal(e.Payload, &s) == nil && !s.IsBusy && s.ID == sid
	})
	var idle Session
	h.do("GET", "/v1/workspaces/"+h.ws+"/sessions/"+sid, nil, &idle)
	if idle.IsBusy {
		t.Fatal("session still busy after cancel")
	}
	// A new run on the same session must be accepted again.
	if code := h.do("POST", "/v1/workspaces/"+h.ws+"/agent", RunRequest{SessionID: sid, RunID: "r2", Prompt: "again"}, nil); code != 202 {
		t.Fatalf("rerun after cancel = %d", code)
	}
}

func TestBusySessionRejectsSecondRun(t *testing.T) {
	h := newHarness(t, ScriptRunner{Pace: time.Millisecond})
	sid := h.startRun("x")
	h.until(func(e sseEvent) bool { return e.Kind == "permission_request" })
	if code := h.do("POST", "/v1/workspaces/"+h.ws+"/agent", RunRequest{SessionID: sid, RunID: "r2", Prompt: "y"}, nil); code != http.StatusConflict {
		t.Fatalf("second run while busy = %d, want 409", code)
	}
}

func TestUnknownRouteIsLoudNotSilent(t *testing.T) {
	h := newHarness(t, ScriptRunner{})
	var out map[string]string
	code := h.do("GET", "/v1/workspaces/"+h.ws+"/definitely/not/there", nil, &out)
	if code != http.StatusNotImplemented || !strings.Contains(out["error"], "not implemented") {
		t.Fatalf("code=%d body=%v", code, out)
	}
}

func TestServerLevelEndpointsAndErrors(t *testing.T) {
	h := newHarness(t, ScriptRunner{})
	var v map[string]string
	if h.do("GET", "/v1/version", nil, &v) != 200 || v["version"] == "" || v["build_id"] == "" {
		t.Fatalf("version: %v", v)
	}
	if code := h.do("GET", "/v1/health", nil, nil); code != 200 {
		t.Fatalf("health %d", code)
	}
	if code := h.do("POST", "/v1/control", map[string]string{"command": "shutdown_if_idle"}, nil); code != http.StatusConflict {
		t.Fatalf("control must be refused with 409, got %d", code)
	}
	if code := h.do("GET", "/v1/workspaces/nope/agent", nil, nil); code != 404 {
		t.Fatalf("unknown workspace = %d", code)
	}
}

func TestFixturesAreCleanAndLoadable(t *testing.T) {
	entries, err := fixtureFS.ReadDir("testdata/fixtures")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		f, err := loadFixture(name)
		if err != nil {
			t.Fatal(err)
		}
		if f.Status != 200 {
			t.Errorf("%s: status %d", name, f.Status)
		}
		// The client/server exchange carries a dump of the process environment (secrets).
		var m map[string]any
		if json.Unmarshal(f.Body, &m) == nil {
			if env, ok := m["env"].([]any); ok && len(env) > 0 {
				t.Errorf("%s: fixture contains an env dump", name)
			}
		}
		if bytes.Contains(f.Body, []byte("sk-")) {
			t.Errorf("%s: looks like it contains an API key", name)
		}
	}
}
