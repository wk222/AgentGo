package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/governance"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type executionContractModel struct{}

func (executionContractModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range messages {
		if msg.Role == schema.Tool {
			return schema.AssistantMessage("finished", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "contract-call", Type: "function", Function: schema.FunctionCall{Name: "code_view", Arguments: "{\"path\":\"demo\",\n}"}}}), nil
}
func (m executionContractModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func (m executionContractModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type executionContractTool struct{ calls *int }

func (executionContractTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "code_view", Desc: "test", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"path": {Type: schema.String, Required: true}})}, nil
}
func (tool executionContractTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	*tool.calls++
	if strings.Contains(args, ",\n}") {
		return "", errors.New("unrepaired arguments reached tool")
	}
	return "", errors.New("actual tool failed after being invoked")
}

func TestRealADKChainPreservesToolFailureForObserversAndHooks(t *testing.T) {
	r := newPipelineTestRunner(t)
	handlers, err := r.BuildADKHandlers(context.Background(), executionContractModel{})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the real framework's middleware application order, not a hand-built chain.
	calls := 0
	var hookErr error
	var seenArgs string
	dispose := governance.RegisterToolHook(governance.ToolHook{Name: "contract", After: func(_ context.Context, c governance.ToolCall, o governance.ToolOutcome) {
		hookErr = o.Err
		seenArgs = c.Arguments
	}})
	defer dispose()
	obs := &recObs{}
	ctx := WithToolObserver(BindRunContext(context.Background(), "contract"), obs)
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "contract", Description: "test", Model: executionContractModel{}, Handlers: handlers, MaxIterations: 3, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{executionContractTool{calls: &calls}}}}})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a})
	it := runner.Run(ctx, []adk.Message{schema.UserMessage("inspect demo")})
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
	if calls != 1 || hookErr == nil || !strings.Contains(hookErr.Error(), "actual tool failed") {
		t.Fatalf("false outcome or repeated execution: calls=%d err=%v", calls, hookErr)
	}
	if strings.Contains(seenArgs, ",\n}") || !strings.Contains(seenArgs, "\"path\"") {
		t.Fatal("governance did not see repaired executable arguments", seenArgs)
	}
	if len(obs.lines) != 2 || !strings.HasSuffix(obs.lines[1], " ERR") {
		t.Fatal("observer reported failed execution as success", obs.lines)
	}
}
