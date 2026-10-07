package crushproto

import (
	"context"
	"strings"
	"testing"
)

// persistRunner narrates one finished turn that also changes a file and links
// the session to an external id.
type persistRunner struct{}

func (persistRunner) Run(_ context.Context, req RunRequest, em *Emitter) error {
	em.UserMessage(req.Prompt)
	a := em.Assistant()
	a.AppendText("hello")
	a.Finish("end_turn")
	em.FileChanged("/p/a.go", "old", "new")
	em.SetLink("agentgo-session-1")
	em.Complete(req, a.ID(), "hello", "my title", 11, 22)
	return nil
}

func (h *harness) recreateWorkspace() string {
	h.t.Helper()
	var resp map[string]any
	h.do("POST", "/v1/workspaces", map[string]any{"path": h.path, "data_dir": h.t.TempDir(), "client_id": newID(), "env": []string{}}, &resp)
	id, _ := resp["id"].(string)
	if id == "" {
		h.t.Fatalf("no workspace id: %v", resp)
	}
	return id
}

// A TUI exit deletes its workspace; reopening the project must find the
// sessions, messages, file history, token usage and the AgentGo link again.
func TestSessionsSurviveWorkspaceRecreation(t *testing.T) {
	store := FileStore{Dir: t.TempDir()}
	h := newHarness(t, persistRunner{}, func(o *Options) { o.Store = store })
	sid := h.startRun("hi")
	h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })

	if code := h.do("DELETE", "/v1/workspaces/"+h.ws, nil, nil); code != 200 {
		t.Fatalf("DELETE workspace = %d", code)
	}
	wid := h.recreateWorkspace()

	var sessions []Session
	h.do("GET", "/v1/workspaces/"+wid+"/sessions", nil, &sessions)
	if len(sessions) != 1 || sessions[0].ID != sid || sessions[0].Title != "my title" ||
		sessions[0].PromptTokens != 11 || sessions[0].CompletionTokens != 22 || sessions[0].IsBusy {
		t.Fatalf("restored sessions = %+v", sessions)
	}
	var msgs []Message
	h.do("GET", "/v1/workspaces/"+wid+"/sessions/"+sid+"/messages", nil, &msgs)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("restored messages = %+v", msgs)
	}
	var files []File
	h.do("GET", "/v1/workspaces/"+wid+"/sessions/"+sid+"/history", nil, &files)
	if len(files) != 2 || files[0].Content != "old" || files[1].Content != "new" {
		t.Fatalf("restored history = %+v", files)
	}
	snap, err := store.Load(h.path)
	if err != nil || snap == nil || snap.Links[sid] != "agentgo-session-1" {
		t.Fatalf("link not persisted: %+v err=%v", snap, err)
	}
}

func TestDeletedSessionIsForgotten(t *testing.T) {
	store := FileStore{Dir: t.TempDir()}
	h := newHarness(t, persistRunner{}, func(o *Options) { o.Store = store })
	sid := h.startRun("hi")
	h.until(func(e sseEvent) bool { return e.Kind == "run_complete" })
	h.do("DELETE", "/v1/workspaces/"+h.ws+"/sessions/"+sid, nil, nil)
	h.do("DELETE", "/v1/workspaces/"+h.ws, nil, nil)

	var sessions []Session
	h.do("GET", "/v1/workspaces/"+h.recreateWorkspace()+"/sessions", nil, &sessions)
	if len(sessions) != 0 {
		t.Fatalf("deleted session came back: %+v", sessions)
	}
	if snap, _ := store.Load(h.path); snap != nil && len(snap.Links) != 0 {
		t.Fatalf("link of deleted session kept: %+v", snap.Links)
	}
}

// If the previous process died mid-run, the half-written assistant message
// must not look like it is still running.
func TestRestoreClosesUnfinishedAssistantMessage(t *testing.T) {
	store := FileStore{Dir: t.TempDir()}
	h := newHarness(t, persistRunner{}, func(o *Options) { o.Store = store })
	h.do("DELETE", "/v1/workspaces/"+h.ws, nil, nil)
	err := store.Save(h.path, &Snapshot{
		Sessions: []Session{{ID: "s1", Title: "t"}},
		Messages: map[string][]*Message{"s1": {
			{ID: "m1", SessionID: "s1", Role: "assistant", Parts: []Part{{Type: "text", Data: map[string]any{"text": "par"}}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []Message
	h.do("GET", "/v1/workspaces/"+h.recreateWorkspace()+"/sessions/s1/messages", nil, &msgs)
	if len(msgs) != 1 || msgs[0].Parts[len(msgs[0].Parts)-1].Type != "finish" {
		t.Fatalf("unfinished message not closed: %+v", msgs)
	}
}

func TestFileStoreKeyIgnoresPathCase(t *testing.T) {
	store := FileStore{Dir: t.TempDir()}
	if err := store.Save(`C:\Proj\App`, &Snapshot{Sessions: []Session{{ID: "x"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(strings.ToLower(`C:\Proj\App`))
	if err != nil || got == nil || len(got.Sessions) != 1 {
		t.Fatalf("load = %+v err=%v", got, err)
	}
	if none, err := store.Load(`C:\Other`); err != nil || none != nil {
		t.Fatalf("unknown path should give (nil,nil), got %+v err=%v", none, err)
	}
}

// Two clients of one project share a workspace; it only goes away with the last one.
func TestWorkspaceIsSharedPerProjectPath(t *testing.T) {
	h := newHarness(t, persistRunner{})
	other := h.recreateWorkspace() // same path, still attached
	if other != h.ws {
		t.Fatalf("second client got workspace %s, want shared %s", other, h.ws)
	}
	if code := h.do("DELETE", "/v1/workspaces/"+h.ws, nil, nil); code != 200 {
		t.Fatalf("first DELETE = %d", code)
	}
	if code := h.do("GET", "/v1/workspaces/"+h.ws, nil, nil); code != 200 {
		t.Fatalf("workspace vanished while a client is attached: %d", code)
	}
	h.do("DELETE", "/v1/workspaces/"+h.ws, nil, nil)
	if code := h.do("GET", "/v1/workspaces/"+h.ws, nil, nil); code != 404 {
		t.Fatalf("workspace should be gone after the last client, got %d", code)
	}
}
