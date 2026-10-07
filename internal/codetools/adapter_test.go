package codetools

import (
	"context"
	"errors"
	"strings"
	"testing"

	ct "github.com/charmbracelet/crush/pkg/codetools"
	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/governance"
)

func fake(name string, run func(ctx context.Context, sid, callID, args string) (ct.Result, error)) ct.Tool {
	return ct.Tool{
		Name: name, Description: "desc of " + name,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "path"},
			},
			"required": []string{"file_path"},
		},
		Run: run,
	}
}

func TestToolNameMapping(t *testing.T) {
	cases := map[string]string{"view": "code_view", "edit": "code_edit", "lsp_diagnostics": "lsp_diagnostics", "grep": "code_grep"}
	for in, want := range cases {
		if got := ToolName(in); got != want {
			t.Errorf("ToolName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, n := range []string{"edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol"} {
		if !IsMutating(n) {
			t.Errorf("%s should be mutating", n)
		}
	}
	if IsMutating("view") || IsMutating("lsp_diagnostics") {
		t.Error("readers must not be flagged mutating")
	}
}

func TestAdaptBuildsEinoToolInfo(t *testing.T) {
	out, err := Adapt([]ct.Tool{fake("view", nil)})
	if err != nil || len(out) != 1 {
		t.Fatalf("adapt: %v %d", err, len(out))
	}
	info, err := out[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "code_view" || info.Desc != "desc of view" {
		t.Fatalf("info = %+v", info)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil || js == nil {
		t.Fatalf("schema: %v", err)
	}
	if _, ok := js.Properties.Get("file_path"); !ok || len(js.Required) != 1 || js.Required[0] != "file_path" {
		t.Fatalf("schema lost properties/required: %+v", js)
	}
}

func TestInvokeRoutesSessionAndShapesResults(t *testing.T) {
	var gotSID, gotArgs string
	ok := fake("view", func(_ context.Context, sid, _, args string) (ct.Result, error) {
		gotSID, gotArgs = sid, args
		return ct.Result{Content: "file body"}, nil
	})
	soft := fake("edit", func(context.Context, string, string, string) (ct.Result, error) {
		return ct.Result{Content: "old_string not found", IsError: true}, nil
	})
	hard := fake("write", func(context.Context, string, string, string) (ct.Result, error) {
		return ct.Result{}, errors.New("disk on fire")
	})
	tools, err := Adapt([]ct.Tool{ok, soft, hard})
	if err != nil {
		t.Fatal(err)
	}
	run := func(i int, ctx context.Context) (string, error) {
		return tools[i].(einotool.InvokableTool).InvokableRun(ctx, `{"file_path":"a.go"}`)
	}

	wantDefault := governance.SessionIDFromContext(context.Background()) // governance supplies the fallback
	if out, err := run(0, context.Background()); err != nil || out != "file body" || gotSID != wantDefault || gotArgs != `{"file_path":"a.go"}` {
		t.Fatalf("default session: out=%q err=%v sid=%q args=%q", out, err, gotSID, gotArgs)
	}
	ctx := governance.WithSessionID(context.Background(), "sess-42")
	if _, err := run(0, ctx); err != nil || gotSID != "sess-42" {
		t.Fatalf("session from ctx: sid=%q err=%v", gotSID, err)
	}

	// Tool-level failures go back to the model as text; infrastructure errors abort.
	if out, err := run(1, ctx); err != nil || !strings.HasPrefix(out, "ERROR: ") || !strings.Contains(out, "not found") {
		t.Fatalf("soft failure: out=%q err=%v", out, err)
	}
	if _, err := run(2, ctx); err == nil || !strings.Contains(err.Error(), "code_write") || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("hard failure: %v", err)
	}
}

func TestAdaptRejectsBrokenSchema(t *testing.T) {
	bad := ct.Tool{Name: "x", Schema: map[string]any{"properties": make(chan int)}}
	if _, err := Adapt([]ct.Tool{bad}); err == nil {
		t.Fatal("unmarshalable schema accepted")
	}
}
