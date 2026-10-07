package bridge

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/workflow"
)

// Full-stack acceptance: workflow(coding_agent) -> AppService -> engine.Router ->
// Crush sidecar -> real model fixes a Go compile error.
//
//	$env:XDG_DATA_HOME = "<empty temp dir>"   # REQUIRED: keeps this off your real AgentGo DB
//	$env:AGENTGO_CRUSH_E2E = "1"; $env:AGENTGO_CRUSH_EXE = "...\crush.exe"
//	$env:AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT
//	$env:AGENTGO_DISABLE_SCHEDULER = "1"
//	go test -run TestE2ECodingAgentWorkflow -v -timeout 10m ./internal/bridge/
func TestE2ECodingAgentWorkflow(t *testing.T) {
	if os.Getenv("AGENTGO_CRUSH_E2E") != "1" {
		t.Skip("set AGENTGO_CRUSH_E2E=1 to run")
	}
	if os.Getenv("XDG_DATA_HOME") == "" {
		t.Skip("set XDG_DATA_HOME to a temp dir so the test does not touch your real AgentGo data")
	}
	if os.Getenv(crushEnvExe) == "" || os.Getenv("AZURE_OPENAI_API_KEY") == "" || os.Getenv("AZURE_OPENAI_API_ENDPOINT") == "" {
		t.Skip("need AGENTGO_CRUSH_EXE and AZURE_OPENAI_API_KEY/ENDPOINT")
	}
	deployment := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if deployment == "" {
		deployment = "gpt-6-luna"
	}

	ws := t.TempDir()
	broken := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tmsg := \"hi\"\n\tfmt.Println(mesage)\n}\n"
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	b, _ := json.Marshal(map[string]any{
		"providers": map[string]any{"azure": map[string]any{
			"api_key": "$AZURE_OPENAI_API_KEY", "api_endpoint": "$AZURE_OPENAI_API_ENDPOINT",
			"models": []any{map[string]any{"id": deployment, "name": deployment, "context_window": 400000, "default_max_tokens": 16384}},
		}},
		"models": map[string]any{
			"large": map[string]any{"provider": "azure", "model": deployment},
			"small": map[string]any{"provider": "azure", "model": deployment},
		},
	})
	if err := os.WriteFile(filepath.Join(cfgDir, "crush.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTGO_CRUSH_CONFIG_DIR", cfgDir)
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())
	if os.Getenv("AZURE_OPENAI_API_VERSION") == "" {
		t.Setenv("AZURE_OPENAI_API_VERSION", "2025-04-01-preview")
	}

	rt, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAppService(rt)
	defer svc.Close()
	if svc.crushEngine() == nil {
		t.Fatal("crush engine was not registered")
	}

	def := workflow.Definition{
		ID: "e2e-fix", Name: "e2e fix compile error",
		Nodes: []workflow.Node{
			{ID: "n_in", Type: "start"},
			{ID: "fix", Type: "coding_agent", Prompt: "{{input}}", Config: map[string]any{"work_dir": ws}},
			{ID: "n_out", Type: "end"},
		},
		Edges: []workflow.Edge{{From: "n_in", To: "fix"}, {From: "fix", To: "n_out"}},
	}
	if r := svc.SaveWorkflow(def); r["success"] != true {
		t.Fatalf("save workflow: %v", r)
	}
	r := svc.RunWorkflow("e2e-fix", "main.go ?????????????,??? `go build ./...` ?????")
	if r["success"] != true {
		t.Fatalf("workflow failed: %v", r)
	}
	out, _ := r["output"].(string)
	t.Logf("node output: %s", out)

	var res workflow.CodingResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("node output is not CodingResult JSON: %v\n%s", err, out)
	}
	if res.ToolCalls == 0 || len(res.ChangedFiles) == 0 || !strings.HasSuffix(filepath.ToSlash(res.ChangedFiles[0].Path), "main.go") {
		t.Errorf("result lacks tool calls / main.go change: %+v", res)
	}
	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		t.Fatalf("project still does not compile: %v\n%s", err, o)
	}
}
