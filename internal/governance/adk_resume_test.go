package governance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// These tests drive the real chain the app uses: ChatModelAgent + the
// governance tool middleware + an ADK checkpoint store, then resume with
// ResumeWithParams. The older middleware tests approve through the queue and
// call the wrapped endpoint again, so they never touch the checkpoint (gob)
// or the resume payload type. Two bugs lived exactly there (unregistered gob
// types; *ResumePayload vs ResumePayload) and both failed silently.

type memCheckpointStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[id]
	return b, ok, nil
}

func (s *memCheckpointStore) Set(_ context.Context, id string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[id] = b
	return nil
}

// scriptedModel asks for one create_tool call, then answers "done" once a tool
// result is present in the history.
type scriptedModel struct{ tool string } // tool defaults to create_tool

func (m scriptedModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, m := range in {
		if m.Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:       "call_1",
		Type:     "function",
		Function: schema.FunctionCall{Name: orDefault(m.tool, "create_tool"), Arguments: `{"path":"a.txt"}`},
	}}), nil
}

func (m scriptedModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m scriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type countingTool struct {
	runs *int32
	name string // defaults to create_tool
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (c countingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: orDefault(c.name, "create_tool"),
		Desc: "test tool that needs approval",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path": {Type: schema.String, Desc: "path", Required: true},
		}),
	}, nil
}

func (c countingTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	atomic.AddInt32(c.runs, 1)
	return "tool_ok", nil
}

type drained struct {
	text        string
	interruptID string
}

func drainAgent(t *testing.T, iter *adk.AsyncIterator[*adk.AgentEvent]) drained {
	t.Helper()
	var d drained
	for {
		ev, ok := iter.Next()
		if !ok {
			return d
		}
		if ev.Err != nil {
			t.Fatalf("agent event error: %v", ev.Err)
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			for _, ic := range ev.Action.Interrupted.InterruptContexts {
				if ic.IsRootCause {
					d.interruptID = ic.ID
				}
			}
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil && ev.Output.MessageOutput.Message != nil {
			if m := ev.Output.MessageOutput.Message; m.Role == schema.Assistant && m.Content != "" {
				d.text += m.Content
			}
		}
	}
}

func TestADKCheckpointResumeAfterApproval(t *testing.T) {
	cases := []struct {
		name         string
		approved     bool
		payload      func(approved bool) any
		wantToolRuns int32
		wantText     string
	}{
		{"approve with value payload", true, func(a bool) any { return ResumePayload{Approved: a} }, 1, "done"},
		{"approve with pointer payload", true, func(a bool) any { return &ResumePayload{Approved: a} }, 1, "done"},
		{"reject with value payload", false, func(a bool) any { return ResumePayload{Approved: a} }, 0, ""},
		{"reject with pointer payload", false, func(a bool) any { return &ResumePayload{Approved: a} }, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			queue, err := NewApprovalQueue(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = queue.Close() })

			var runs int32
			store := &memCheckpointStore{}
			policy := Policy{ToolRiskLevels: map[string]RiskLevel{"create_tool": RiskHigh}}
			// A fresh agent and runner for each phase mirrors the app: resume
			// happens in a new call, possibly after a restart.
			newRunner := func() *adk.Runner {
				agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
					Name:        "test",
					Description: "test",
					Model:       scriptedModel{},
					ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
						Tools:               []tool.BaseTool{countingTool{runs: &runs}},
						ToolCallMiddlewares: []compose.ToolMiddleware{ComposeToolMiddleware(queue, policy)},
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
			}

			first := drainAgent(t, newRunner().Run(ctx, []adk.Message{schema.UserMessage("make a.txt")}, adk.WithCheckPointID("cp")))
			if first.interruptID == "" {
				t.Fatal("expected the agent to pause for approval")
			}
			if n := atomic.LoadInt32(&runs); n != 0 {
				t.Fatalf("tool ran %d times before approval", n)
			}
			pending, err := queue.ListPending(ctx, nil)
			if err != nil || len(pending) != 1 {
				t.Fatalf("pending=%d err=%v", len(pending), err)
			}
			if err := queue.Resolve(ctx, pending[0].ID, tc.approved, "test", "tester", &ResumePayload{Approved: tc.approved}); err != nil {
				t.Fatal(err)
			}

			iter, err := newRunner().ResumeWithParams(ctx, "cp", &adk.ResumeParams{
				Targets: map[string]any{first.interruptID: tc.payload(tc.approved)},
			})
			if err != nil {
				t.Fatal(err)
			}
			second := drainAgent(t, iter)

			if second.interruptID != "" {
				t.Fatalf("resume paused again (id=%s): the decision was not delivered to the tool", second.interruptID)
			}
			if got := atomic.LoadInt32(&runs); got != tc.wantToolRuns {
				t.Fatalf("tool ran %d times after resume, want %d", got, tc.wantToolRuns)
			}
			if tc.approved && second.text != tc.wantText {
				t.Fatalf("final answer %q, want %q", second.text, tc.wantText)
			}
		})
	}
}
