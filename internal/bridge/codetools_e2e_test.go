package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/engine"
)

// Acceptance: AgentGo's own Eino agent (no Crush sidecar) uses the Crush code
// tools in-process to find and fix a compile error with a real model.
//
//	$env:XDG_DATA_HOME = "<empty temp dir>"     # REQUIRED: keeps this off your real data
//	$env:AGENTGO_CODETOOLS_E2E = "1"
//	$env:AGENTGO_DISABLE_SCHEDULER = "1"
//	$env:AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT   # e.g. https://x.openai.azure.com/
//	$env:AGENTGO_AZURE_DEPLOYMENT = "gpt-6-luna"            # optional
//	go test -run TestE2EEinoAgentFixesCodeWithCodetools -v -timeout 10m ./internal/bridge/
func TestE2EEinoAgentFixesCodeWithCodetools(t *testing.T) {
	if os.Getenv("AGENTGO_CODETOOLS_E2E") != "1" {
		t.Skip("set AGENTGO_CODETOOLS_E2E=1 to run")
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
	deployment := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if deployment == "" {
		deployment = "gpt-6-luna"
	}
	t.Setenv(crushEnvExe, filepath.Join(t.TempDir(), "no-crush")) // prove Crush's own loop is not involved

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
	// Azure reasoning deployments reject function tools on chat/completions otherwise.
	if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
		t.Setenv("AGENTGO_REASONING_EFFORT", "none")
	}
	// Diagnostic: what the agent would actually be handed in the default mode.
	var visible []string
	for _, bt := range agent.RegistryToolsForMode(rt.toolReg, agent.DefaultSessionMode()) {
		if info, err := bt.Info(context.Background()); err == nil {
			visible = append(visible, info.Name)
		}
	}
	t.Logf("static tools visible to agent (default mode): %v", visible)
	base := strings.TrimRight(endpoint, "/") + "/openai/v1/"
	if err := rt.SetLLMConfig(LLMConfig{APIBase: base, APIKey: key, Model: deployment}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	counts := map[engine.EventType]int{}
	sink := engine.SinkFunc(func(e engine.Event) { mu.Lock(); counts[e.Type]++; mu.Unlock() })
	res := s.RunEngineSync(EinoEngineID, "codetools-e2e", 
		"工作区根目录下的 main.go 有编译错误。请使用 lsp_ 和 code_ 开头的代码工具先定位错误,再做最小改动修复它。修复后用 lsp_diagnostics 确认没有错误,然后简短汇报。", nil, sink)

	mu.Lock()
	t.Logf("result: err=%q pending=%v events=%v", res.Error, res.Pending, counts)
	mu.Unlock()
	for _, m := range res.Messages {
		t.Logf("[%s/%s] %s", m.Role, m.Type, m.Content)
	}
	if res.Error != "" {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Pending {
		t.Fatalf("run stopped waiting for approval (governance); see log above")
	}
	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		b, _ := os.ReadFile(file)
		t.Fatalf("project still does not compile: %v\n%s\n--- main.go ---\n%s", err, o, b)
	}
}
