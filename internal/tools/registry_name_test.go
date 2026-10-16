package tools

import (
	"context"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type nameIn struct {
	X string `json:"x"`
}

func mkTool(t *testing.T, name string) einotool.BaseTool {
	t.Helper()
	tl, err := utils.InferTool(name, "d", func(ctx context.Context, in nameIn) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func TestAddToolSkipsInvalidNames(t *testing.T) {
	r := NewRegistry()
	r.AddTool(mkTool(t, "good_tool-1.x"))
	r.AddTool(mkTool(t, "workflow_预览_happy_path"))
	r.AddTool(mkTool(t, "has space"))

	if _, ok := r.tools["good_tool-1.x"]; !ok {
		t.Fatal("valid tool must be registered")
	}
	if len(r.tools) != 1 {
		t.Fatalf("invalid names must be skipped, got %d tools", len(r.tools))
	}
	for _, name := range []string{"ok", "a.b-c_d", "A1"} {
		if !ValidToolName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "预览", "a b", "a/b", "a:b"} {
		if ValidToolName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}
