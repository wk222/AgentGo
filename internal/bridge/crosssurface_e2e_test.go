package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/crushproto"
	"agentgo/internal/hostlink"
	"agentgo/internal/sessions"
)

// Acceptance (real model), across surfaces: a TUI attached to the host through
// its authenticated endpoint starts a run that fixes a project; the desktop
// side approves what the run asks for; afterwards the run's facts are checked
// from every angle — the TUI's transcript and file history, the approval
// records, the run-event log, and the project itself.
//
// Gating (nothing here touches real data or prints a key):
//
//	$env:AGENTGO_CROSSSURFACE_E2E = "1"
//	$env:XDG_DATA_HOME = "<an empty temp dir>"
//	AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT   (existing model settings)
//	gopls on PATH
func TestE2ECrossSurfaceFixWithDesktopApproval(t *testing.T) {
	if os.Getenv("AGENTGO_CROSSSURFACE_E2E") != "1" {
		t.Skip("set AGENTGO_CROSSSURFACE_E2E=1 to run")
	}
	if os.Getenv("XDG_DATA_HOME") == "" {
		t.Skip("set XDG_DATA_HOME to a temp dir")
	}
	key, endpoint := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	if key == "" || endpoint == "" {
		t.Skip("need AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
	deployment := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if deployment == "" {
		deployment = "gpt-6-luna"
	}
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", "")
	t.Setenv("AGENTGO_CRUSHPROTO_YOLO", "") // approvals must really be asked
	t.Setenv(hostlinkEnv, "")
	t.Setenv(crushEnvExe, t.TempDir()+"/no-crush")
	if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
		t.Setenv("AGENTGO_REASONING_EFFORT", "none")
	}

	ws := t.TempDir()
	mustWrite(t, ws+"/go.mod", "module e2e\n\ngo 1.21\n")
	mustWrite(t, ws+"/main.go", brokenGo)

	// ---- the host: what the desktop app is
	rt, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAppService(rt)
	defer svc.Close()
	if _, err := rt.SetWorkspaceRoot(ws); err != nil {
		t.Fatal(err)
	}
	if r := svc.ReloadPlugin("codetools"); r["success"] != true {
		t.Fatalf("codetools plugin: %v", r)
	}
	if err := rt.SetLLMConfig(LLMConfig{APIBase: trimSlash(endpoint) + "/openai/v1/", APIKey: key, Model: deployment}); err != nil {
		t.Fatal(err)
	}

	// ---- the TUI: finds the host like agentgo-tui does and goes through the proxy
	info, err := hostlink.Discover(context.Background(), rt.DataDir())
	if err != nil {
		t.Fatalf("the host must be discoverable: %v", err)
	}
	front := httptest.NewServer(hostlink.Proxy(info))
	defer front.Close()
	base := front.URL + "/v1"
	do := func(method, path string, body, out any) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
	}
	var wsResp map[string]any
	do("POST", "/workspaces", map[string]any{"path": ws, "data_dir": t.TempDir(), "client_id": "44444444-4444-4444-8444-444444444444", "env": []string{}}, &wsResp)
	wid, _ := wsResp["id"].(string)
	if wid == "" {
		t.Fatalf("no workspace: %v", wsResp)
	}

	evResp, err := http.Get(base + "/workspaces/" + wid + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer evResp.Body.Close()
	var (
		mu       sync.Mutex
		requests int // permission dialogs the TUI was shown
		closed   int // dialogs the TUI saw closed with "granted"
		complete = make(chan struct{})
	)
	go func() {
		sc := bufio.NewScanner(evResp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			switch {
			case strings.Contains(line, `"permission_request"`):
				requests++
			case strings.Contains(line, `"permission_notification"`) && strings.Contains(line, `"granted":true`):
				closed++
			}
			mu.Unlock()
			if strings.Contains(line, `"run_complete"`) {
				close(complete)
				return
			}
		}
	}()

	// ---- the desktop: approves whatever is pending, as the approvals panel would
	approved := map[string]string{} // approval id -> tool
	stop := make(chan struct{})
	desktopDone := make(chan struct{})
	go func() {
		defer close(desktopDone)
		for {
			select {
			case <-stop:
				return
			case <-time.After(300 * time.Millisecond):
			}
			pend, err := rt.Approvals().ListPending(context.Background(), nil)
			if err != nil {
				continue
			}
			for _, p := range pend {
				mu.Lock()
				_, done := approved[p.ID]
				mu.Unlock()
				if done {
					continue
				}
				mu.Lock()
				approved[p.ID] = p.Summary
				mu.Unlock()
				svc.ResolveApproval(p.ID, true, "approved from the desktop")
			}
		}
	}()

	var sess crushproto.Session
	do("POST", "/workspaces/"+wid+"/sessions", crushproto.Session{Title: "non-interactive"}, &sess)
	do("POST", "/workspaces/"+wid+"/agent", crushproto.RunRequest{
		SessionID: sess.ID, RunID: "r1",
		Prompt: "工作区根目录下的 main.go 有编译错误。请用 lsp_ 和 code_ 开头的代码工具定位并做最小改动修复,然后用命令行执行 `go build ./...` 确认编译通过,最后简短汇报。",
	}, nil)
	select {
	case <-complete:
	case <-time.After(8 * time.Minute):
		t.Fatal("run did not complete")
	}
	close(stop)
	<-desktopDone
	time.Sleep(500 * time.Millisecond)

	// 1) the project is really fixed
	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		t.Fatalf("project still does not compile: %v\n%s", err, o)
	}

	// 2) cross-surface approval actually happened, and the TUI's dialogs followed it
	mu.Lock()
	t.Logf("approvals taken by the desktop: %v ; TUI dialogs shown=%d closed-as-granted=%d", approved, requests, closed)
	nApproved := len(approved)
	mu.Unlock()
	if nApproved == 0 {
		t.Fatal("the run never asked for approval, so the scenario did not exercise the desktop->TUI path (use a prompt/governance that needs one)")
	}
	mu.Lock()
	if closed < nApproved {
		t.Errorf("TUI dialogs closed = %d, want at least %d (one per desktop approval)", closed, nApproved)
	}
	mu.Unlock()

	// 3) approval records say who decided
	for id := range approved {
		req, err := rt.Approvals().GetRequest(context.Background(), id)
		if err != nil || req == nil {
			t.Fatalf("approval %s: %v", id, err)
		}
		if req.Status != "approved" || req.ResolvedBy != "desktop_user" {
			t.Errorf("approval %s recorded as status=%q resolvedBy=%q", id, req.Status, req.ResolvedBy)
		}
	}

	// 4) the TUI's own view: tool calls paired with results, file history before/after
	var msgs []struct {
		Parts []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"parts"`
	}
	do("GET", "/workspaces/"+wid+"/sessions/"+sess.ID+"/messages", nil, &msgs)
	calls, results := map[string]string{}, map[string]bool{}
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch p.Type {
			case "tool_call":
				id, _ := p.Data["id"].(string)
				name, _ := p.Data["name"].(string)
				calls[id] = name
			case "tool_result":
				id, _ := p.Data["tool_call_id"].(string)
				results[id] = true
			}
		}
	}
	t.Logf("TUI view: tool calls %v", calls)
	for id, name := range calls {
		if !results[id] {
			t.Errorf("TUI view: call %s (%s) has no result", id, name)
		}
	}
	var files []crushproto.File
	do("GET", "/workspaces/"+wid+"/sessions/"+sess.ID+"/history", nil, &files)
	if len(files) < 2 || files[0].Content != brokenGo {
		t.Errorf("TUI file history lacks before/after of main.go (%d versions)", len(files))
	}

	// 5) the run-event log: the run is closed, nothing left awaiting approval
	list, err := rt.Sessions().List(context.Background(), 20)
	if err != nil || len(list) == 0 {
		t.Fatalf("no AgentGo session behind the TUI run: %v", err)
	}
	sid := list[0].ID
	evs, err := rt.Sessions().ListRunEvents(context.Background(), sid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("run-event log kinds: %s", kinds(evs))
	st, err := rt.Sessions().LastRunState(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("last run state: %s %s", st.Status, st.Error)
	if st.Status != sessions.RunCompleted {
		t.Errorf("run is %q after completion (error %q)", st.Status, st.Error)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
