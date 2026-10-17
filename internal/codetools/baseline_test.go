package codetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ct "agentgo/third_party/crush/pkg/codetools"
	einotool "github.com/cloudwego/eino/components/tool"
)

// Behavior baseline for the Crush-backed tools (plan-crush-digestion D0). The
// native replacements in D2/D3 must pass these same assertions, so they pin
// observable behavior (what the model sees and what lands on disk), not the
// Crush implementation.

func baselineTools(t *testing.T) (dir string, tools map[string]einotool.BaseTool) {
	t.Helper()
	dir = t.TempDir()
	tb, err := ct.New(ct.Options{WorkDir: dir, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tb.Close)
	adapted, err := Adapt(tb.Tools)
	if err != nil {
		t.Fatal(err)
	}
	tools = map[string]einotool.BaseTool{}
	for _, tl := range adapted {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		tools[info.Name] = tl
	}
	return dir, tools
}

type invokable interface {
	InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error)
}

func runTool(t *testing.T, tools map[string]einotool.BaseTool, name, args string) string {
	t.Helper()
	tl, ok := tools[name].(invokable)
	if !ok {
		t.Fatalf("tool %s missing or not invokable", name)
	}
	out, err := tl.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestBaselineViewEditGrepWrite(t *testing.T) {
	dir, tools := baselineTools(t)
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("hello baseline\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := runTool(t, tools, "code_view", `{"file_path":`+quote(file)+`}`); !strings.Contains(out, "hello baseline") {
		t.Fatalf("view output: %q", out)
	}
	runTool(t, tools, "code_edit", `{"file_path":`+quote(file)+`,"old_string":"hello baseline","new_string":"hello digested"}`)
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "hello digested") {
		t.Fatalf("edit did not land on disk: %q", b)
	}
	if out := runTool(t, tools, "code_grep", `{"pattern":"digested","path":`+quote(dir)+`}`); !strings.Contains(out, "a.txt") {
		t.Fatalf("grep output: %q", out)
	}
	newFile := filepath.Join(dir, "b.txt")
	runTool(t, tools, "code_write", `{"file_path":`+quote(newFile)+`,"content":"fresh\n"}`)
	if b, _ := os.ReadFile(newFile); string(b) != "fresh\n" {
		t.Fatalf("write landed: %q", b)
	}
}

func TestBaselineDiagnosticsNeverEmpty(t *testing.T) {
	_, tools := baselineTools(t)
	// Even with no language server able to analyse the workspace, the model must
	// get an explanatory non-empty answer (see emptyDiagnostics).
	if out := runTool(t, tools, "lsp_diagnostics", `{}`); strings.TrimSpace(out) == "" {
		t.Fatal("diagnostics returned empty output")
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
