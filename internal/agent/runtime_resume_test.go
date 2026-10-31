package agent

import (
	"agentgo/internal/checkpoint"
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"path/filepath"
	"testing"
)

type resumeProtocolModel struct{ runtimeCaptureModel }

func (m *resumeProtocolModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, in)
	for _, msg := range in {
		if msg.Role == schema.Tool && msg.ToolCallID == "approval-call" {
			return schema.AssistantMessage("approved and executed", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "approval-call", Function: schema.FunctionCall{Name: "approval_action", Arguments: "{}"}}}), nil
}
func (m *resumeProtocolModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func TestRuntimeSessionCheckpointResumesAfterDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "resume.db")
	s, db := testRuntimeStore(t, file)
	scope := s.scope
	sid := "approval-chat"
	cpID := checkpoint.CheckpointIDForSession(sid)
	executed := 0
	type args struct{}
	action, err := utils.InferTool("approval_action", "An action requiring approval", func(ctx context.Context, _ args) (string, error) {
		target, has, data := tool.GetResumeContext[string](ctx)
		if target && has && data == "allow" {
			executed++
			return "executed once", nil
		}
		return "", tool.StatefulInterrupt(ctx, "approval required", "pending")
	})
	if err != nil {
		t.Fatal(err)
	}
	build := func(store *runtimeSessionStore, host *Runner, bootstrap bool) (*adk.Runner, *resumeProtocolModel) {
		m := &resumeProtocolModel{}
		ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "test", Description: "resume test", Model: m, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{action}}}})
		if err != nil {
			t.Fatal(err)
		}
		host.runtimeSessions = store
		cfg, err := host.sessionRunnerConfig(ctx, sid, ag, false, bootstrap)
		if err != nil {
			t.Fatal(err)
		}
		return adk.NewRunner(ctx, cfg), m
	}
	cp, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	host := &Runner{cpStore: cp}
	runner, _ := build(s, host, true)
	it := runner.Query(ctx, "perform approved action", adk.WithCheckPointID(cpID))
	interruptID := ""
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if e.Err != nil {
			t.Fatal(e.Err)
		}
		if e.Action != nil && e.Action.Interrupted != nil {
			for _, ic := range e.Action.Interrupted.InterruptContexts {
				interruptID = ic.ID
			}
		}
	}
	if interruptID == "" || executed != 0 {
		t.Fatalf("missing interrupt or early execution: id=%q count=%d", interruptID, executed)
	}
	db.Close()
	restored, reopened := testRuntimeStore(t, file)
	restored.scope = scope
	cp, err = checkpoint.OpenSQLiteStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	host = &Runner{cpStore: cp}
	runner, m := build(restored, host, false)
	it, err = runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{Targets: map[string]any{interruptID: "allow"}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if e.Err != nil {
			t.Fatal(e.Err)
		}
	}
	if executed != 1 || len(m.inputs) != 1 {
		t.Fatalf("repeated or missing action: executed=%d modelCalls=%d", executed, len(m.inputs))
	}
	users, results := 0, 0
	for _, msg := range m.inputs[0] {
		if msg.Role == schema.User && msg.Content == "perform approved action" {
			users++
		}
		if msg.Role == schema.Tool && msg.ToolCallID == "approval-call" {
			results++
		}
	}
	if users != 1 || results != 1 {
		t.Fatalf("invalid resumed context users=%d results=%d", users, results)
	}
}
