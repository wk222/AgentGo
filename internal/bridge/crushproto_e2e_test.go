package bridge

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Acceptance: the STOCK Crush client (`crush run`) talks to AgentGo's crushproto
// plugin, and AgentGo's Eino agent (with the codetools plugin) fixes a compile
// error on its behalf. Crush supplies only the UI; the brain is AgentGo.
//
//	$env:XDG_DATA_HOME = "<empty temp dir>"     # REQUIRED
//	$env:AGENTGO_CRUSHPROTO_E2E = "1"
//	$env:AGENTGO_DISABLE_SCHEDULER = "1"
//	$env:AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT
//	$env:AGENTGO_CRUSH_EXE = "<path to crush.exe>"   # optional, defaults to ..\crush\crush.exe
//	go test -run TestE2ECrushClientDrivesEino -v -timeout 12m ./internal/bridge/
func TestE2ECrushClientDrivesEino(t *testing.T) {
	if os.Getenv("AGENTGO_CRUSHPROTO_E2E") != "1" {
		t.Skip("set AGENTGO_CRUSHPROTO_E2E=1 to run")
	}
	if os.Getenv("XDG_DATA_HOME") == "" {
		t.Skip("set XDG_DATA_HOME to a temp dir so the test does not touch your real AgentGo data")
	}
	key, endpoint := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	if key == "" || endpoint == "" {
		t.Skip("need AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
	crushExe := os.Getenv("AGENTGO_CRUSH_EXE")
	if crushExe == "" {
		crushExe, _ = filepath.Abs(filepath.Join("..", "..", "..", "crush", "crush.exe"))
	}
	if _, err := os.Stat(crushExe); err != nil {
		t.Skipf("crush executable not found: %s", crushExe)
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
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", addr) // set BEFORE NewAppService: the plugin is opt-in at init
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
	base := trimSlash(endpoint) + "/openai/v1/"
	if err := rt.SetLLMConfig(LLMConfig{APIBase: base, APIKey: key, Model: deployment}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, crushExe, "run", "-H", "tcp://"+addr, "-D", t.TempDir(),
		"工作区根目录下的 main.go 有编译错误。请使用 lsp_ 和 code_ 开头的代码工具先定位错误,再做最小改动修复它,修复后简短汇报。")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "CRUSH_CLIENT_SERVER=1")
	out, err := cmd.CombinedOutput()
	t.Logf("crush run output:\n%s", out)
	if err != nil {
		t.Fatalf("crush run: %v", err)
	}
	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		b, _ := os.ReadFile(file)
		t.Fatalf("project still does not compile: %v\n%s\n--- main.go ---\n%s", err, o, b)
	}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
