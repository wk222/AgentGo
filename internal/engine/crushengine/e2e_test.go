package crushengine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/engine"
)

// End-to-end acceptance: a real Crush sidecar + a real model fix a Go compile
// error through AgentGo's engine.Router.
//
//	$env:AGENTGO_CRUSH_E2E = "1"
//	$env:AGENTGO_CRUSH_EXE = "C:\...\crush\crush.exe"       # default: ..\..\..\crush\crush.exe
//	$env:AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT   # model credentials
//	$env:AGENTGO_AZURE_DEPLOYMENT = "gpt-6-luna"            # optional
//	go test -run TestE2E -v -timeout 10m ./internal/engine/crushengine/
//
// Credentials are only read from the environment; nothing is written to disk
// except a temp crush.json that references them as $VARS.
const brokenMain = `package main

import "fmt"

func main() {
	msg := greet("world")
	fmt.Println(mesage)
}

func greet(name string) string {
	return "hello, " + name
}
`

type collectSink struct {
	mu sync.Mutex
	ev []engine.Event
}

func (c *collectSink) Emit(e engine.Event) { c.mu.Lock(); c.ev = append(c.ev, e); c.mu.Unlock() }

func (c *collectSink) count(t engine.EventType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.ev {
		if e.Type == t {
			n++
		}
	}
	return n
}

func TestE2EFixCompileError(t *testing.T) {
	if os.Getenv("AGENTGO_CRUSH_E2E") != "1" {
		t.Skip("set AGENTGO_CRUSH_E2E=1 to run (needs crush.exe + model credentials)")
	}
	exe := os.Getenv("AGENTGO_CRUSH_EXE")
	if exe == "" {
		exe, _ = filepath.Abs(filepath.Join("..", "..", "..", "..", "crush", "crush.exe"))
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("crush executable not found: %s", exe)
	}
	if os.Getenv("AZURE_OPENAI_API_KEY") == "" || os.Getenv("AZURE_OPENAI_API_ENDPOINT") == "" {
		t.Skip("AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT not set")
	}
	deployment := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if deployment == "" {
		deployment = "gpt-6-luna"
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	// Project under test.
	ws := t.TempDir()
	must(t, os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(ws, "main.go"), []byte(brokenMain), 0o644))
	if out, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err == nil {
		t.Fatalf("fixture should not compile, but did: %s", out)
	}

	// Isolated Crush config: model only, no MCP servers, secrets via $VARS.
	cfgDir := t.TempDir()
	conf := map[string]any{
		"providers": map[string]any{"azure": map[string]any{
			"api_key":      "$AZURE_OPENAI_API_KEY",
			"api_endpoint": "$AZURE_OPENAI_API_ENDPOINT",
			"models": []any{map[string]any{
				"id": deployment, "name": deployment, "context_window": 400000, "default_max_tokens": 16384,
			}},
		}},
		"models": map[string]any{
			"large": map[string]any{"provider": "azure", "model": deployment},
			"small": map[string]any{"provider": "azure", "model": deployment},
		},
	}
	b, _ := json.Marshal(conf)
	must(t, os.WriteFile(filepath.Join(cfgDir, "crush.json"), b, 0o644))

	env := []string{"CRUSH_GLOBAL_DATA=" + t.TempDir()}
	if os.Getenv("AZURE_OPENAI_API_VERSION") == "" {
		env = append(env, "AZURE_OPENAI_API_VERSION=2025-04-01-preview")
	}
	ce := New(Config{
		Exe: exe, ConfigDir: cfgDir, ExtraEnv: env,
		LogPath:     filepath.Join(t.TempDir(), "crush-server.log"),
		AutoApprove: true,
	})
	defer ce.Close()

	r := engine.NewRouter()
	r.Register(ce)
	sink := &collectSink{}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	res := r.Run(ctx, engine.RunRequest{
		Engine: EngineID, SessionID: "e2e", Workspace: ws,
		Input: "main.go 有编译错误。请修复它(只做最小改动),然后运行 `go build ./...` 确认编译通过。",
	}, sink)

	t.Logf("result: err=%q msgs=%d tokens=%d tool_calls=%d tool_results=%d file_changes=%d approvals=%d",
		res.Error, len(res.Messages), sink.count(engine.EventToken), sink.count(engine.EventToolCall),
		sink.count(engine.EventToolResult), sink.count(engine.EventFileChange), sink.count(engine.EventApprovalRequest))
	if len(res.Messages) > 0 {
		t.Logf("final: %s", res.Messages[0].Content)
	}
	if res.Error != "" {
		t.Fatalf("run failed: %s", res.Error)
	}

	// 1) The project really compiles and prints the expected greeting.
	if out, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		t.Fatalf("project still does not compile: %v\n%s", err, out)
	}
	out, err := exec.Command("go", "run", "-C", ws, ".").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "hello, world" {
		t.Fatalf("unexpected program output %q (err=%v)", out, err)
	}

	// 2) The unified event stream carried the work.
	if sink.count(engine.EventToolCall) == 0 || sink.count(engine.EventToolResult) == 0 {
		t.Errorf("expected tool_call/tool_result events")
	}
	if sink.count(engine.EventFileChange) == 0 {
		t.Errorf("expected a file_change event for the edit")
	}
	if sink.count(engine.EventDone) != 1 {
		t.Errorf("expected exactly one done event, got %d", sink.count(engine.EventDone))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
