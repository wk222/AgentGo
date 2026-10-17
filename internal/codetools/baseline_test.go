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

// Behavior baseline for the code tools (plan-crush-digestion D0): what the
// model sees and what lands on disk. The native tools must satisfy it; with
// -tags crushparity the vendored Crush tools run the same assertions.

// crushBoxFactory builds the Crush-backed toolbox; only set under the
// crushparity build tag (crush_parity_test.go).
var crushBoxFactory func(t *testing.T, dir string) coderuntime.Toolbox

// nativeBox assembles the production toolbox the way the plugin does. Rg off:
// the Go walker is deterministic.
func nativeBox(t *testing.T, dir string) coderuntime.Toolbox {
	t.Helper()
	search := SearchOptions{WorkDir: dir, Rg: func() string { return "" }}
	lsp, err := codelsp.New(codelsp.Options{WorkDir: dir, Search: SymbolSearch(search)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lsp.Close)
	return Overlay(lsp, append(NativeFS(lsp, dir, nil), NativeSearch(search)...))
}

// eachImpl runs fn against the native tools and, when built with the
// crushparity tag, against the Crush ones too.
func eachImpl(t *testing.T, fn func(t *testing.T, native bool)) {
	if crushBoxFactory != nil {
		t.Run("crush", func(t *testing.T) { fn(t, false) })
	}
	t.Run("native", func(t *testing.T) { fn(t, true) })
}

func baselineTools(t *testing.T, native bool) (dir string, tools map[string]einotool.BaseTool) {
	t.Helper()
	dir = t.TempDir()
	var box coderuntime.Toolbox
	if native {
		box = nativeBox(t, dir)
	} else {
		box = crushBoxFactory(t, dir)
	}
	adapted, err := Adapt(box.Tools())
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
	eachImpl(t, baselineViewEditGrepWrite)
}

func baselineViewEditGrepWrite(t *testing.T, native bool) {
	dir, tools := baselineTools(t, native)
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
	eachImpl(t, func(t *testing.T, native bool) {
		baselineDiagnosticsNeverEmpty(t, native)
	})
}

func baselineDiagnosticsNeverEmpty(t *testing.T, native bool) {
	_, tools := baselineTools(t, native)
	// Even with no language server able to analyse the workspace, the model must
	// get an explanatory non-empty answer (see emptyDiagnostics).
	if out := runTool(t, tools, "lsp_diagnostics", `{}`); strings.TrimSpace(out) == "" {
		t.Fatal("diagnostics returned empty output")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
