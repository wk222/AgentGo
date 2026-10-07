package codetools

import (
	"context"
	"strings"
	"testing"

	ct "github.com/charmbracelet/crush/pkg/codetools"
	einotool "github.com/cloudwego/eino/components/tool"
)

func emptyRun(context.Context, string, string, string) (ct.Result, error) {
	return ct.Result{Content: ""}, nil
}

// An empty lsp_diagnostics result is ambiguous (clean vs. no language server);
// the model must be told so, while other tools keep the generic placeholder.
func TestEmptyDiagnosticsIsExplained(t *testing.T) {
	tools, err := Adapt([]ct.Tool{fake("lsp_diagnostics", emptyRun), fake("grep", emptyRun)})
	if err != nil {
		t.Fatal(err)
	}
	run := func(i int) string {
		out, err := tools[i].(einotool.InvokableTool).InvokableRun(context.Background(), `{"file_path":"a.go"}`)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := run(0); !strings.Contains(got, "no language server") {
		t.Fatalf("diagnostics placeholder not explanatory: %q", got)
	}
	if got := run(1); got != "(no output)" {
		t.Fatalf("grep placeholder = %q", got)
	}
}
