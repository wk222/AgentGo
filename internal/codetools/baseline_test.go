package codetools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/codelsp"
	"agentgo/internal/coderuntime"
)

// nativeBox assembles the production toolbox the way the plugin does. Rg is off:
// the Go walker is deterministic.
func nativeBox(t *testing.T, dir string) coderuntime.Toolbox {
	t.Helper()
	// The pre-overwrite backup is covered by coderuntime/fs_guard_test.go; keep it
	// out of these tests so a temp dir holds only what the test wrote.
	t.Setenv("AGENTGO_OVERWRITE_BACKUP", "0")
	search := SearchOptions{WorkDir: dir, Rg: func() string { return "" }}
	lsp, err := codelsp.New(codelsp.Options{WorkDir: dir, Search: SymbolSearch(search)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lsp.Close)
	return Overlay(lsp, append(NativeFS(lsp, dir, nil), NativeSearch(search)...))
}

// adaptedTools returns the toolbox's tools keyed by the name the model sees.
func adaptedTools(t *testing.T) (dir string, tools map[string]einotool.BaseTool) {
	t.Helper()
	dir = t.TempDir()
	adapted, err := Adapt(nativeBox(t, dir).Tools())
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

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// What the model sees and what lands on disk, through the full adapter stack.
func TestFileToolsRoundTripThroughTheAdapter(t *testing.T) {
	dir, tools := adaptedTools(t)
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("hello world\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := runTool(t, tools, "code_view", `{"file_path":`+quote(file)+`}`); !strings.Contains(out, "hello world") {
		t.Fatalf("view output: %q", out)
	}
	runTool(t, tools, "code_edit", `{"file_path":`+quote(file)+`,"old_string":"hello world","new_string":"hello edited"}`)
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "hello edited") {
		t.Fatalf("edit did not land on disk: %q", b)
	}
	if out := runTool(t, tools, "code_grep", `{"pattern":"edited","path":`+quote(dir)+`}`); !strings.Contains(out, "a.txt") {
		t.Fatalf("grep output: %q", out)
	}
	fresh := filepath.Join(dir, "b.txt")
	runTool(t, tools, "code_write", `{"file_path":`+quote(fresh)+`,"content":"fresh\n"}`)
	if b, _ := os.ReadFile(fresh); string(b) != "fresh\n" {
		t.Fatalf("write landed: %q", b)
	}
}

func TestDiagnosticsAreNeverEmpty(t *testing.T) {
	_, tools := adaptedTools(t)
	// Even with no language server able to analyse the workspace, the model must
	// get an explanatory non-empty answer (see emptyDiagnostics).
	if out := runTool(t, tools, "lsp_diagnostics", `{}`); strings.TrimSpace(out) == "" {
		t.Fatal("diagnostics returned empty output")
	}
}
