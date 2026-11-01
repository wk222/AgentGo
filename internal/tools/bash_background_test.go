package tools

import (
	"context"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
)

func runBashTool(t *testing.T, r *Registry, args string) string {
	t.Helper()
	tl, ok := r.tools["execute_bash"].(einotool.InvokableTool)
	if !ok {
		t.Fatal("execute_bash is not registered as an invokable tool")
	}
	out, err := tl.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("execute_bash: %v", err)
	}
	return out
}

func TestExecuteBashRunInBackgroundUsesStarter(t *testing.T) {
	r := NewRegistry()
	if err := registerExecuteBash(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var gotCmd string
	var work func(context.Context) (string, error)
	r.SetBackgroundBash(func(_ context.Context, command string, w func(context.Context) (string, error)) (string, error) {
		gotCmd, work = command, w
		return "task_42", nil
	})

	out := runBashTool(t, r, `{"command":"echo from-bg","run_in_background":true}`)
	if !strings.Contains(out, "task_42") || !strings.Contains(out, "task_output") {
		t.Fatalf("the reply must carry the task ID and say how to collect it: %s", out)
	}
	if strings.Contains(out, "from-bg") {
		t.Fatalf("the command ran in the foreground: %s", out)
	}
	if gotCmd != "echo from-bg" || work == nil {
		t.Fatalf("starter got command %q, work %v", gotCmd, work != nil)
	}
	// What the background task finally produces is the same JSON the foreground
	// call would have returned.
	res, err := work(context.Background())
	if err != nil {
		t.Fatalf("background work: %v", err)
	}
	if !strings.Contains(res, "from-bg") || !strings.Contains(res, `"exit_code":0`) {
		t.Fatalf("background result: %s", res)
	}
}

func TestExecuteBashRunInBackgroundFallsBackToForeground(t *testing.T) {
	r := NewRegistry() // no starter: a runtime without background tasks
	if err := registerExecuteBash(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out := runBashTool(t, r, `{"command":"echo still-runs","run_in_background":true}`)
	if !strings.Contains(out, "still-runs") {
		t.Fatalf("without background support the command must still run: %s", out)
	}
}

func TestExecuteBashForegroundIgnoresStarter(t *testing.T) {
	r := NewRegistry()
	if err := registerExecuteBash(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	r.SetBackgroundBash(func(context.Context, string, func(context.Context) (string, error)) (string, error) {
		t.Fatal("a command that did not ask for the background must not be started there")
		return "", nil
	})
	if out := runBashTool(t, r, `{"command":"echo plain"}`); !strings.Contains(out, "plain") {
		t.Fatalf("foreground result: %s", out)
	}
}
