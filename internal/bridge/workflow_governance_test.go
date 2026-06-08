package bridge

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
)

type stubWfTool struct{}

func (stubWfTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "get_current_time"}, nil
}

func (stubWfTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return `{"time":"ok"}`, nil
}

func TestWorkflowToolInvokeUsesRegistry(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cpStore, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	reg := tools.NewRegistry()
	reg.AddTool(stubWfTool{})
	rt := &Runtime{toolReg: reg, approvals: queue, cpStore: cpStore}
	bridge, err := rt.buildWorkflowToolsBridge(context.Background(), nil)
	if err != nil || bridge == nil {
		t.Fatalf("bridge: %v", err)
	}
	out, err := rt.workflowToolInvoke(context.Background(), "get_current_time", "{}")
	if err != nil || out == "" {
		t.Fatalf("invoke: %q err=%v", out, err)
	}
}
