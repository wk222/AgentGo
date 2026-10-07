package crushproto

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// askRunner asks for one permission that another surface may decide first, and
// records who decided and what it did afterwards.
type askRunner struct {
	other   chan bool
	decided chan [2]bool // {granted, byOther}
}

func (r *askRunner) Run(ctx context.Context, req RunRequest, em *Emitter) error {
	em.UserMessage(req.Prompt)
	granted, byOther := em.AwaitPermission(PermissionRequest{
		ID: "perm-1", ToolCallID: "call-1", ToolName: "bash", Action: "bash",
	}, r.other)
	r.decided <- [2]bool{granted, byOther}
	a := em.Assistant()
	a.AppendText("continued")
	a.Finish("end_turn")
	em.Complete(req, a.ID(), "continued", "t", 0, 0)
	return nil
}

func isPermissionDone(granted bool) func(sseEvent) bool {
	return func(e sseEvent) bool {
		if e.Kind != "permission_notification" {
			return false
		}
		var p struct {
			Granted bool `json:"granted"`
			Denied  bool `json:"denied"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		return p.Granted == granted && p.Denied == !granted
	}
}

// A decision taken on another surface wakes the waiting run, and the client's
// own dialog is closed by the same notification a local answer would produce.
func TestPermissionCanBeDecidedByAnotherSurface(t *testing.T) {
	r := &askRunner{other: make(chan bool, 1), decided: make(chan [2]bool, 1)}
	h := newHarness(t, r)
	h.startRun("do it")
	h.until(func(e sseEvent) bool { return e.Kind == "permission_request" })

	r.other <- true // e.g. the desktop app approves

	h.until(isPermissionDone(true)) // TUI dialog closes
	select {
	case got := <-r.decided:
		if got != [2]bool{true, true} {
			t.Fatalf("decision = granted:%v byOther:%v, want true/true", got[0], got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not continue")
	}
	h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })

	// Nothing is left waiting: a late answer from the client is a no-op.
	var res map[string]bool
	h.do("POST", "/v1/workspaces/"+h.ws+"/permissions/grant",
		map[string]any{"action": "deny", "permission": PermissionRequest{ID: "perm-1"}}, &res)
	if res["resolved"] {
		t.Fatal("a decision that was already taken must not be applied twice")
	}
}

// A client that reconnects while a request is still waiting is shown it again;
// otherwise the run would wait forever behind a dialog nobody can see.
func TestReconnectingClientSeesOutstandingPermission(t *testing.T) {
	r := &askRunner{other: make(chan bool, 1), decided: make(chan [2]bool, 1)}
	h := newHarness(t, r)
	h.startRun("do it")
	h.until(func(e sseEvent) bool { return e.Kind == "permission_request" })

	// Second connection (the first one stays up, as another viewer would).
	resp, err := http.Get(h.ts.URL + "/v1/workspaces/" + h.ws + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				lines <- sc.Text()
			}
		}
	}()
	select {
	case l := <-lines:
		if !strings.Contains(l, `"permission_request"`) || !strings.Contains(l, "perm-1") {
			t.Fatalf("first frame on reconnect = %s, want the outstanding permission_request", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconnected client was not told about the outstanding permission")
	}

	// Once decided it is no longer replayed.
	r.other <- false
	<-r.decided
	h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })
	resp2, err := http.Get(h.ts.URL + "/v1/workspaces/" + h.ws + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp2.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case l := <-got:
		t.Fatalf("a decided permission was replayed: %s", l)
	case <-time.After(300 * time.Millisecond):
	}
}
