package bridge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/crushproto"
)

// Acceptance (real model): over plain HTTP, exactly what a Crush client sees
// after AgentGo's Eino agent fixes a compile error — paired tool calls/results,
// file history for the "Modified Files" sidebar and token usage.
//
// Same gating as TestE2ECrushClientDrivesEino (AGENTGO_CRUSHPROTO_E2E=1, XDG_DATA_HOME, Azure env, gopls).
func TestE2ECrushprotoStateAfterFix(t *testing.T) {
	if os.Getenv("AGENTGO_CRUSHPROTO_E2E") != "1" {
		t.Skip("set AGENTGO_CRUSHPROTO_E2E=1 to run")
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", addr)
	t.Setenv("AGENTGO_CRUSHPROTO_YOLO", "1")
	t.Setenv(crushEnvExe, filepath.Join(t.TempDir(), "no-crush"))
	if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
		t.Setenv("AGENTGO_REASONING_EFFORT", "none")
	}

	ws := t.TempDir()
	file := filepath.Join(ws, "main.go")
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(brokenGo), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	s := NewAppService(rt)
	defer s.Close()
	if _, err := rt.SetWorkspaceRoot(ws); err != nil {
		t.Fatal(err)
	}
	if r := s.ReloadPlugin("codetools"); r["success"] != true {
		t.Fatalf("codetools plugin: %v", r)
	}
	if err := rt.SetLLMConfig(LLMConfig{APIBase: trimSlash(endpoint) + "/openai/v1/", APIKey: key, Model: deployment}); err != nil {
		t.Fatal(err)
	}

	base := "http://" + addr + "/v1"
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
	do("POST", "/workspaces", map[string]any{"path": ws, "data_dir": t.TempDir(), "client_id": "22222222-2222-4222-8222-222222222222", "env": []string{}}, &wsResp)
	wid, _ := wsResp["id"].(string)
	if wid == "" {
		t.Fatalf("no workspace: %v", wsResp)
	}

	// Subscribe before running so run_complete cannot be missed.
	evResp, err := http.Get(base + "/workspaces/" + wid + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer evResp.Body.Close()
	complete := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(evResp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if strings.Contains(sc.Text(), `"run_complete"`) {
				close(complete)
				return
			}
		}
	}()

	var sess crushproto.Session
	do("POST", "/workspaces/"+wid+"/sessions", crushproto.Session{Title: "non-interactive"}, &sess)
	do("POST", "/workspaces/"+wid+"/agent", crushproto.RunRequest{
		SessionID: sess.ID, RunID: "r1",
		Prompt: "工作区根目录下的 main.go 有编译错误。请使用 lsp_ 和 code_ 开头的代码工具先定位错误,再做最小改动修复它,修复后简短汇报。",
	}, nil)
	select {
	case <-complete:
	case <-time.After(5 * time.Minute):
		t.Fatal("run did not complete")
	}
	time.Sleep(500 * time.Millisecond) // late stream-usage callback

	// 1) every tool call is answered by a result with the same id
	var msgs []struct {
		Role  string `json:"role"`
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
	t.Logf("tool calls: %v ; results: %v", calls, results)
	if len(calls) == 0 {
		t.Fatal("no tool calls in message snapshots")
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("tool call %q has no matching result", id)
		}
	}

	// 2) Modified Files data
	var files []crushproto.File
	do("GET", "/workspaces/"+wid+"/sessions/"+sess.ID+"/history", nil, &files)
	t.Logf("history: %d versions", len(files))
	if len(files) < 2 || files[0].Version != 0 || files[0].Content != brokenGo {
		t.Errorf("history does not hold before/after of main.go: %+v", files)
	}

	// 3) token usage
	var got crushproto.Session
	do("GET", "/workspaces/"+wid+"/sessions/"+sess.ID, nil, &got)
	t.Logf("tokens: prompt=%d completion=%d", got.PromptTokens, got.CompletionTokens)
	if got.PromptTokens == 0 {
		t.Errorf("session token usage not reported")
	}

	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		t.Fatalf("project still does not compile: %v\n%s", err, o)
	}
}
