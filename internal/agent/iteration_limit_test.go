package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// loopingModel asks for the same tool on every call, so any run with a finite
// iteration budget ends by exhausting it.
type loopingModel struct{ calls int }

func (m *loopingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: "c" + string(rune('0'+m.calls)), Function: schema.FunctionCall{Name: "noop", Arguments: "{}"},
	}}), nil
}

func (m *loopingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *loopingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

func TestRunThatExhaustsItsIterationBudgetEndsGracefully(t *testing.T) {
	ctx := context.Background()
	type none struct{}
	noop, err := utils.InferTool("noop", "does nothing", func(context.Context, none) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	m := &loopingModel{}
	ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "t", Description: "t", Model: m, MaxIterations: 3,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{noop}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	iter := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag}).Query(ctx, "go")

	var streamed strings.Builder
	r := &Runner{}
	content, pause, _, err := r.drainADKEvents(ctx, iter, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("hitting the step limit must not be an error (the work is done): %v", err)
	}
	if pause != nil {
		t.Fatalf("unexpected pause: %+v", pause)
	}
	if m.calls != 3 {
		t.Fatalf("model was called %d times, want exactly the budget (3)", m.calls)
	}
	if !strings.Contains(content, "步数上限") || !strings.Contains(streamed.String(), "步数上限") {
		t.Fatalf("the user must be told the run stopped at the limit; content=%q streamed=%q", content, streamed.String())
	}
}

func TestHitIterationLimitOnlyMatchesTheBudgetError(t *testing.T) {
	if !hitIterationLimit(adk.ErrExceedMaxIterations) {
		t.Fatal("the sentinel itself must match")
	}
	if hitIterationLimit(context.Canceled) || hitIterationLimit(nil) {
		t.Fatal("other errors are real failures")
	}
}
