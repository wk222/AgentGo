package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type executionOutcomeModel struct{ seen []string }

func (m *executionOutcomeModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	for _, msg := range messages {
		if msg.Role == schema.Tool {
			m.seen = append(m.seen, msg.Content)
		}
	}
	return (executionContractModel{}).Generate(ctx, messages, opts...)
}
func (m *executionOutcomeModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}
func (m *executionOutcomeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type contractExecutionPlan struct {
	Steps []string `json:"steps"`
}

func init() {
	schema.RegisterName[*contractExecutionPlan]("agentgo_test_execution_contract_plan")
}

func (p *contractExecutionPlan) FirstStep() string { return p.Steps[0] }
func (p *contractExecutionPlan) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Steps []string `json:"steps"`
	}{p.Steps})
}
func (p *contractExecutionPlan) UnmarshalJSON(raw []byte) error {
	var value struct {
		Steps []string `json:"steps"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	p.Steps = value.Steps
	return nil
}

func sharedContractAgent(t *testing.T, ctx context.Context, r *Runner, kind string, m *executionOutcomeModel, action tool.BaseTool) (adk.Agent, []adk.AgentRunOption) {
	t.Helper()
	cfg := adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{action}}}
	var a adk.Agent
	var err error
	var opts []adk.AgentRunOption
	switch kind {
	case "chat":
		handlers, e := r.BuildADKHandlers(ctx, m)
		if e != nil {
			t.Fatal(e)
		}
		a, err = adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "contract", Description: "test", Model: m, Handlers: handlers, ToolsConfig: cfg, MaxIterations: 3})
	case "specialist":
		a, err = adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "contract", Description: "test", Model: m, Handlers: r.toolExecutionHandlers(ctx).all(), ToolsConfig: cfg, MaxIterations: 3})
	case "deep":
		a, err = deep.New(ctx, &deep.Config{Name: "contract", Description: "test", ChatModel: m, Handlers: r.toolExecutionHandlers(ctx).all(), ToolsConfig: cfg, MaxIteration: 3, WithoutWriteTodos: true, WithoutGeneralSubAgent: true})
	case "plan-executor":
		cfg.ToolCallMiddlewares = []compose.ToolMiddleware{r.toolExecutionHandlers(ctx).compose()}
		a, err = planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{Model: m, ToolsConfig: cfg, MaxIterations: 3})
		opts = append(opts, adk.WithSessionValues(map[string]any{planexecute.PlanSessionKey: &contractExecutionPlan{Steps: []string{"inspect demo"}}, planexecute.UserInputSessionKey: []adk.Message{schema.UserMessage("inspect demo")}}))
	}
	if err != nil {
		t.Fatal(err)
	}
	return a, opts
}

func TestExecutionContractIsTheSameAcrossAgentKinds(t *testing.T) {
	for _, kind := range []string{"chat", "specialist", "deep", "plan-executor"} {
		t.Run(kind, func(t *testing.T) {
			r := newPipelineTestRunner(t)
			obs := &recObs{}
			meter := &RunMeter{}
			ctx := WithRunMeter(WithToolObserver(BindRunContext(context.Background(), "shared-contract"), obs), meter)
			calls, hookCalls := 0, 0
			var hookErr error
			var args string
			dispose := governance.RegisterToolHook(governance.ToolHook{Name: "shared-contract", After: func(ctx context.Context, c governance.ToolCall, out governance.ToolOutcome) {
				hookCalls++
				hookErr = out.Err
				args = c.Arguments
				if governance.SessionIDFromContext(ctx) != "shared-contract" {
					t.Error("governance scope lost")
				}
			}})
			defer dispose()
			m := &executionOutcomeModel{}
			a, opts := sharedContractAgent(t, ctx, r, kind, m, executionContractTool{calls: &calls})
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a}).Query(ctx, "inspect demo", opts...)
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			if calls != 1 || hookCalls != 1 || hookErr == nil || !strings.Contains(hookErr.Error(), "actual tool failed") {
				t.Fatalf("different execution semantics: calls=%d hooks=%d err=%v", calls, hookCalls, hookErr)
			}
			if strings.Contains(args, ",\n}") {
				t.Fatal("unrepaired arguments reached governance")
			}
			if len(obs.lines) != 2 || !strings.HasSuffix(obs.lines[1], " ERR") || meter.Snapshot().ToolCalls != 1 {
				t.Fatalf("missing/duplicate events or metrics: %v %+v", obs.lines, meter.Snapshot())
			}
			if len(m.seen) != 1 || !strings.Contains(m.seen[0], "[tool error]") {
				t.Fatal("model did not receive the shared error result", m.seen)
			}
		})
	}
}

func TestBlockedToolsCannotBypassSharedExecution(t *testing.T) {
	for _, kind := range []string{"chat", "specialist", "deep", "plan-executor"} {
		t.Run(kind, func(t *testing.T) {
			r := newPipelineTestRunner(t)
			r.policy.BlockedTools = map[string]bool{"code_view": true}
			ctx := BindRunContext(context.Background(), "blocked")
			calls := 0
			m := &executionOutcomeModel{}
			a, opts := sharedContractAgent(t, ctx, r, kind, m, executionContractTool{calls: &calls})
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a}).Query(ctx, "inspect demo", opts...)
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			if calls != 0 || len(m.seen) != 1 || !strings.Contains(m.seen[0], "blocked") {
				t.Fatalf("blocked tool executed: calls=%d model=%v", calls, m.seen)
			}
		})
	}
}

func TestSharedExecutionApprovalResumesExactlyOnce(t *testing.T) {
	for _, kind := range []string{"chat", "specialist", "deep", "plan-executor"} {
		t.Run(kind, func(t *testing.T) {
			r := newPipelineTestRunner(t)
			r.policy.ToolRiskLevels = map[string]governance.RiskLevel{"code_view": governance.RiskCritical}
			r.policy.MaxDailyBudget = 1
			r.policy.Control = governance.ControlPolicyFromMode("balanced")
			r.policy.Control.MaxCallsPerTool = 1
			_, db := testRuntimeStore(t, filepath.Join(t.TempDir(), "approval.db"))
			cp, err := checkpoint.OpenSQLiteStore(db)
			if err != nil {
				t.Fatal(err)
			}
			obs, meter := &recObs{}, &RunMeter{}
			ctx := WithRunMeter(WithToolObserver(BindRunContext(context.Background(), "approval-contract"), obs), meter)
			calls := 0
			m := &executionOutcomeModel{}
			a, opts := sharedContractAgent(t, ctx, r, kind, m, executionContractTool{calls: &calls})
			runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a, CheckPointStore: cp})
			opts = append(opts, adk.WithCheckPointID("contract"))
			it := runner.Query(ctx, "inspect demo", opts...)
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
			pending, err := r.queue.ListPending(ctx, nil)
			if err != nil || len(pending) != 1 || interruptID == "" || calls != 0 || len(obs.lines) != 0 || meter.Snapshot().ToolCalls != 0 {
				t.Fatalf("approval did not pause execution: pending=%v id=%q calls=%d events=%v err=%v", pending, interruptID, calls, obs.lines, err)
			}
			args, _ := pending[0].Metadata["arguments"].(string)
			var approvedArgs map[string]string
			if err := json.Unmarshal([]byte(args), &approvedArgs); err != nil || approvedArgs["path"] != "demo" {
				t.Fatalf("approval did not record executable arguments: %q %v", args, err)
			}
			payload := &governance.ResumePayload{Approved: true}
			if err := r.queue.Resolve(ctx, pending[0].ID, true, "test approve", "tester", payload); err != nil {
				t.Fatal(err)
			}
			// Resume through newly constructed agents and a fresh execution
			// context: middleware-local budget/counters cannot make this pass.
			resumeCtx := WithRunMeter(WithToolObserver(BindRunContext(context.Background(), "approval-contract"), obs), meter)
			a, _ = sharedContractAgent(t, resumeCtx, r, kind, m, executionContractTool{calls: &calls})
			runner = adk.NewRunner(resumeCtx, adk.RunnerConfig{Agent: a, CheckPointStore: cp})
			it, err = runner.ResumeWithParams(resumeCtx, "contract", &adk.ResumeParams{Targets: map[string]any{interruptID: payload}})
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
			if calls != 1 || len(obs.lines) != 2 || meter.Snapshot().ToolCalls != 1 || len(m.seen) != 1 || !strings.Contains(m.seen[0], "actual tool failed") {
				t.Fatalf("resume lost/repeated execution: calls=%d events=%v meter=%+v model=%v", calls, obs.lines, meter.Snapshot(), m.seen)
			}
			// The checkpoint restored the original root's call accounting, not
			// merely a new scope with the same conversation ID.
			probe := &executionOutcomeModel{}
			a, opts = sharedContractAgent(t, ctx, r, kind, probe, executionContractTool{calls: &calls})
			it = adk.NewRunner(ctx, adk.RunnerConfig{Agent: a}).Query(ctx, "inspect again", opts...)
			for {
				e, ok := it.Next()
				if !ok {
					break
				}
				if e.Err != nil {
					t.Fatal(e.Err)
				}
			}
			if calls != 1 || len(probe.seen) != 1 || !strings.Contains(probe.seen[0], "速率上限") {
				t.Fatalf("resume lost original root limit: calls=%d model=%v", calls, probe.seen)
			}
		})
	}
}

func TestCanvasCannotRelaxHostGovernanceCaps(t *testing.T) {
	r := newPipelineTestRunner(t)
	r.policy = governance.BuildPolicy("strict", r.workspaceRoot)
	for _, canvas := range []ExecutionCanvas{CanvasFocused, CanvasBalanced, CanvasDeep} {
		p := r.effectivePolicyForCtx(WithCanvasPolicy(context.Background(), CanvasPolicyFor(canvas)))
		if p.Control.MaxCallsPerTool > r.policy.Control.MaxCallsPerTool || p.Control.MaxSubagentDepth > r.policy.Control.MaxSubagentDepth || p.Control.StuckLoopKillThreshold > r.policy.Control.StuckLoopKillThreshold {
			t.Fatalf("%s relaxed host policy: %+v", canvas, p.Control)
		}
	}
}
