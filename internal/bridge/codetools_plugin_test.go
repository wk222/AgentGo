package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/capability"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

const brokenGo = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tmsg := \"hi\"\n\tfmt.Println(mesage)\n}\n"

func registryTool(t *testing.T, reg *tools.Registry, name string) einotool.InvokableTool {
	t.Helper()
	for _, bt := range reg.GetAllTools() {
		info, _ := bt.Info(context.Background())
		if info != nil && info.Name == name {
			return bt.(einotool.InvokableTool)
		}
	}
	t.Fatalf("tool %q not in registry", name)
	return nil
}

func invoke(t *testing.T, tool einotool.InvokableTool, args map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := tool.InvokableRun(ctx, string(b))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	return out
}

func newCodetoolsService(t *testing.T, workspace string) (*AppService, *tools.Registry, *capability.Bus) {
	t.Helper()
	t.Setenv(crushEnvExe, filepath.Join(t.TempDir(), "no-crush"))
	reg := tools.NewRegistry()
	bus := capability.NewBus()
	rt := &Runtime{capBus: bus, toolReg: reg, workspace: workspace, dataDir: t.TempDir()}
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	return s, reg, bus
}

func TestCodetoolsPluginContributesAndRetiresTools(t *testing.T) {
	s, reg, bus := newCodetoolsService(t, t.TempDir())
	if pluginState(t, s, "codetools") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	for _, n := range []string{"code_view", "code_edit", "lsp_diagnostics", "code_grep"} {
		registryTool(t, reg, n)
		if st, ok := grantStatus(bus, "tool", "codetools", n); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", n, st, ok)
		}
	}
	s.StopPlugin("codetools")
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(context.Background()); strings.HasPrefix(info.Name, "code_") || strings.HasPrefix(info.Name, "lsp_") {
			t.Fatalf("tool %s survived plugin stop", info.Name)
		}
	}
	if st, _ := grantStatus(bus, "tool", "codetools", "code_edit"); st != capability.StatusRetired {
		t.Fatalf("grant after stop = %q", st)
	}
	if r := s.ReloadPlugin("codetools"); r["success"] != true {
		t.Fatalf("reload: %v", r)
	}
	registryTool(t, reg, "code_edit")
}

func TestCodetoolsPluginNeedsAWorkspaceAndCanBeDisabled(t *testing.T) {
	s, _, _ := newCodetoolsService(t, "")
	if st := pluginState(t, s, "codetools"); st != plugin.StateFailed {
		t.Fatalf("no workspace => failed, got %s", st)
	}
	t.Setenv("AGENTGO_CODETOOLS", "0")
	s2, _, _ := newCodetoolsService(t, t.TempDir())
	for _, st := range s2.ListPlugins() {
		if st.Name == "codetools" {
			t.Fatalf("disabled plugin must not be registered: %+v", st)
		}
	}
}

// The in-process LSP self-fix loop, driven through the Eino tool surface.
func TestCodetoolsDiagnoseAndFixThroughRegistry(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(ws, "main.go")
	if err := os.WriteFile(file, []byte(brokenGo), 0o644); err != nil {
		t.Fatal(err)
	}
	_, reg, _ := newCodetoolsService(t, ws)

	if out := invoke(t, registryTool(t, reg, "code_view"), map[string]any{"file_path": file}); !strings.Contains(out, "mesage") {
		t.Fatalf("view: %s", out)
	}
	diag := registryTool(t, reg, "lsp_diagnostics")
	var out string
	for i := 0; i < 10 && !strings.Contains(out, "mesage"); i++ {
		out = invoke(t, diag, map[string]any{"file_path": file})
		time.Sleep(time.Second)
	}
	if !strings.Contains(out, "mesage") {
		t.Fatalf("diagnostics missed the error:\n%s", out)
	}
	res := invoke(t, registryTool(t, reg, "code_edit"), map[string]any{
		"file_path": file, "old_string": "fmt.Println(mesage)", "new_string": "fmt.Println(msg)",
	})
	if strings.HasPrefix(res, "ERROR") {
		t.Fatalf("edit: %s", res)
	}
	if o, err := exec.Command("go", "build", "-C", ws, "./...").CombinedOutput(); err != nil {
		t.Fatalf("still broken: %v\n%s", err, o)
	}
}
