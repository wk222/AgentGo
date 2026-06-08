package workflow

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type stubInvokableTool struct {
	name string
	out  string
}

func (s stubInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s stubInvokableTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	return s.out + ":" + args, nil
}

func TestToolsBridgeInvoke(t *testing.T) {
	ctx := context.Background()
	st := stubInvokableTool{name: "echo_wf", out: "ok"}
	bridge, err := NewToolsBridge(ctx, ToolsBridgeConfig{Tools: []tool.BaseTool{st}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := bridge.Invoke(ctx, "echo_wf", `{"x":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `ok:{"x":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestRunContextInvokeToolUsesRunner(t *testing.T) {
	ctx := context.Background()
	st := stubInvokableTool{name: "via_bridge", out: "b"}
	bridge, err := NewToolsBridge(ctx, ToolsBridgeConfig{Tools: []tool.BaseTool{st}})
	if err != nil {
		t.Fatal(err)
	}
	rc := &RunContext{
		ToolRunner: bridge,
	}
	got, err := rc.InvokeTool(ctx, "via_bridge", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "b:{}" {
		t.Fatalf("got %q", got)
	}
}

func TestRunContextInvokeToolUsesRunnerFunc(t *testing.T) {
	rc := &RunContext{
		ToolRunner: ToolRunnerFunc(func(context.Context, string, string) (string, error) {
			return "legacy", nil
		}),
	}
	got, err := rc.InvokeTool(context.Background(), "fallback", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "legacy" {
		t.Fatalf("got %q", got)
	}
}
