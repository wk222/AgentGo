package tools

import (
	"context"
	"fmt"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type namedTool struct{ name string }

func (n namedTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: n.name, Desc: n.name}, nil
}

var _ einotool.BaseTool = namedTool{}

func names(ts []einotool.BaseTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		i, _ := t.Info(context.Background())
		out = append(out, i.Name)
	}
	return out
}

// The tool list is part of the prompt prefix. Random map order changed it on every
// model call and made prefix caching impossible (baseline: ~1% hit rate).
func TestRegistryToolListOrderIsDeterministic(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 40; i++ {
		r.AddTool(namedTool{name: fmt.Sprintf("zz_dyn_tool_%02d", (i*7)%40)})
	}
	r.AddTool(namedTool{name: "echo_message"})
	r.AddTool(namedTool{name: "get_current_time"})
	r.AddTool(namedTool{name: "list_workspace_dir"})

	want := map[string][]string{
		"all":     names(r.GetAllTools()),
		"static":  names(r.GetStaticTools()),
		"dynamic": names(r.GetDynamicTools()),
	}
	if len(want["static"]) != 3 || len(want["dynamic"]) != 40 {
		t.Fatalf("fixture split wrong: static=%d dynamic=%d", len(want["static"]), len(want["dynamic"]))
	}
	for i := 0; i < 50; i++ {
		for kind, get := range map[string]func() []einotool.BaseTool{
			"all": r.GetAllTools, "static": r.GetStaticTools, "dynamic": r.GetDynamicTools,
		} {
			got := names(get())
			for j := range got {
				if got[j] != want[kind][j] {
					t.Fatalf("%s order changed between calls at %d: %q vs %q", kind, j, got[j], want[kind][j])
				}
			}
		}
	}
	for kind, list := range want {
		for j := 1; j < len(list); j++ {
			if list[j-1] > list[j] {
				t.Fatalf("%s list is not sorted: %q before %q", kind, list[j-1], list[j])
			}
		}
	}
}
