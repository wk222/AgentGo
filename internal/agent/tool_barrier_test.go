package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"agentgo/internal/governance"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// events records when probe tools start and end, and how many ran at once.
type barrierEvents struct {
	mu        sync.Mutex
	list      []string
	active    int
	maxActive int
}

func (e *barrierEvents) begin(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, "start:"+name)
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
}

func (e *barrierEvents) end(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, "end:"+name)
	e.active--
}

func (e *barrierEvents) at(event string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.list {
		if s == event {
			return i
		}
	}
	return -1
}

type barrierProbe struct {
	name string
	hold time.Duration
	fail bool
	ev   *barrierEvents
}

func (p barrierProbe) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: p.name, Desc: "probe", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}

func (p barrierProbe) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	p.ev.begin(p.name)
	defer p.ev.end(p.name)
	select {
	case <-time.After(p.hold):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if p.fail {
		return "", errors.New("probe failed")
	}
	return "ok", nil
}

// batchModel answers once with the whole batch of calls, then finishes.
type batchModel struct{ calls []schema.ToolCall }

func (m batchModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range messages {
		if msg.Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("", m.calls), nil
}

func (m batchModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m batchModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

func declareProbeTools(t *testing.T) {
	t.Helper()
	for name, traits := range map[string]governance.ToolTraits{
		"bar_read_a": {Source: "test"},
		"bar_read_b": {Source: "test", Risk: governance.RiskLow},
		"bar_read_c": {Source: "test"},
		"bar_write":  {Source: "test", Mutating: true, Risk: governance.RiskMedium},
		"bar_risky":  {Source: "test", Risk: governance.RiskMedium}, // not mutating, but not low risk
	} {
		t.Cleanup(governance.RegisterToolTraits(name, traits))
	}
	// "bar_shell" is deliberately undeclared, like execute_bash.
}

// runBatch runs one model reply holding the given calls through a real ADK agent.
func runBatch(t *testing.T, handlers []adk.ChatModelAgentMiddleware, ev *barrierEvents, probes []barrierProbe) {
	t.Helper()
	calls := make([]schema.ToolCall, len(probes))
	tools := make([]tool.BaseTool, len(probes))
	for i, p := range probes {
		p.ev = ev
		probes[i] = p
		calls[i] = schema.ToolCall{ID: fmt.Sprintf("%s-%d", t.Name(), i), Type: "function", Function: schema.FunctionCall{Name: p.name, Arguments: "{}"}}
		tools[i] = p
	}
	ctx, cancel := context.WithTimeout(BindRunContext(context.Background(), t.Name()), 20*time.Second)
	defer cancel()
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "barrier", Description: "test", Model: batchModel{calls: calls}, Handlers: handlers, MaxIterations: 3,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
	})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a}).Run(ctx, []adk.Message{schema.UserMessage("go")})
	for {
		event, ok := it.Next()
		if !ok {
			return
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
}

func barrierHandlers(t *testing.T) []adk.ChatModelAgentMiddleware {
	t.Helper()
	b := NewToolBarrierMiddleware()
	if b == nil {
		t.Fatal("barrier is off by default")
	}
	return []adk.ChatModelAgentMiddleware{NewSafeToolMiddleware(), b}
}

func TestBarrierRunsAWriteBeforeTheShellThatFollowsIt(t *testing.T) {
	declareProbeTools(t)
	ev := &barrierEvents{}
	runBatch(t, barrierHandlers(t), ev, []barrierProbe{
		{name: "bar_write", hold: 150 * time.Millisecond},
		{name: "bar_shell"},
	})
	if w, s := ev.at("end:bar_write"), ev.at("start:bar_shell"); w < 0 || s < 0 || w > s {
		t.Fatalf("shell started before the write finished: %v", ev.list)
	}
}

// Without the barrier the framework starts the last-listed call first. This is
// what the test above guards against, shown here so it cannot pass by accident.
func TestWithoutTheBarrierTheShellStartsBeforeTheWriteEnds(t *testing.T) {
	declareProbeTools(t)
	t.Setenv("AGENTGO_TOOL_BARRIER", "0")
	if NewToolBarrierMiddleware() != nil {
		t.Fatal("AGENTGO_TOOL_BARRIER=0 did not turn the barrier off")
	}
	ev := &barrierEvents{}
	runBatch(t, []adk.ChatModelAgentMiddleware{NewSafeToolMiddleware()}, ev, []barrierProbe{
		{name: "bar_write", hold: 150 * time.Millisecond},
		{name: "bar_shell"},
	})
	if w, s := ev.at("end:bar_write"), ev.at("start:bar_shell"); w < 0 || s < 0 || s > w {
		t.Fatalf("expected the old, overlapping behaviour: %v", ev.list)
	}
}

func TestBarrierLeavesReadOnlyCallsRunningTogether(t *testing.T) {
	declareProbeTools(t)
	ev := &barrierEvents{}
	start := time.Now()
	runBatch(t, barrierHandlers(t), ev, []barrierProbe{
		{name: "bar_read_a", hold: 200 * time.Millisecond},
		{name: "bar_read_b", hold: 200 * time.Millisecond},
		{name: "bar_read_c", hold: 200 * time.Millisecond},
	})
	if ev.maxActive != 3 {
		t.Fatalf("read-only calls did not overlap, max at once %d: %v", ev.maxActive, ev.list)
	}
	if time.Since(start) > 550*time.Millisecond {
		t.Fatalf("read-only calls ran one after another: %v", time.Since(start))
	}
}

func TestBarrierSeparatesReadsFromAWriteInTheMiddle(t *testing.T) {
	declareProbeTools(t)
	ev := &barrierEvents{}
	runBatch(t, barrierHandlers(t), ev, []barrierProbe{
		{name: "bar_read_a", hold: 120 * time.Millisecond},
		{name: "bar_read_b", hold: 120 * time.Millisecond},
		{name: "bar_write"},
		{name: "bar_read_c"},
	})
	w0, w1 := ev.at("start:bar_write"), ev.at("end:bar_write")
	if ev.at("end:bar_read_a") > w0 || ev.at("end:bar_read_b") > w0 {
		t.Fatalf("an earlier read was still running when the write began: %v", ev.list)
	}
	if ev.at("start:bar_read_c") < w1 {
		t.Fatalf("a later read began before the write finished: %v", ev.list)
	}
	if ev.at("start:bar_read_b") > ev.at("end:bar_read_a") {
		t.Fatalf("the two reads before the write no longer overlap: %v", ev.list)
	}
}

func TestBarrierTreatsANonLowRiskToolAsOrdered(t *testing.T) {
	declareProbeTools(t)
	ev := &barrierEvents{}
	runBatch(t, barrierHandlers(t), ev, []barrierProbe{
		{name: "bar_risky", hold: 120 * time.Millisecond},
		{name: "bar_read_a"},
	})
	if ev.at("end:bar_risky") > ev.at("start:bar_read_a") {
		t.Fatalf("a medium-risk call overlapped the call after it: %v", ev.list)
	}
}

func TestBarrierLetsLaterCallsRunAfterAnEarlierOneFails(t *testing.T) {
	declareProbeTools(t)
	ev := &barrierEvents{}
	runBatch(t, barrierHandlers(t), ev, []barrierProbe{
		{name: "bar_write", hold: 80 * time.Millisecond, fail: true},
		{name: "bar_shell"},
	})
	if ev.at("end:bar_write") < 0 || ev.at("end:bar_shell") < 0 {
		t.Fatalf("a failed call blocked the call after it: %v", ev.list)
	}
	if ev.at("end:bar_write") > ev.at("start:bar_shell") {
		t.Fatalf("order not kept after a failure: %v", ev.list)
	}
}

func TestParallelSafeToolNeedsADeclaration(t *testing.T) {
	declareProbeTools(t)
	cases := map[string]bool{
		"bar_read_a": true,
		"bar_read_b": true,
		"bar_write":  false,
		"bar_risky":  false,
		"bar_shell":  false, // undeclared
		"":           false,
	}
	for name, want := range cases {
		if got := parallelSafeTool(name); got != want {
			t.Errorf("parallelSafeTool(%q) = %v, want %v", name, got, want)
		}
	}
}

func planFor(t *testing.T, ctx context.Context, names ...string) []schema.ToolCall {
	t.Helper()
	calls := make([]schema.ToolCall, len(names))
	for i, n := range names {
		calls[i] = schema.ToolCall{ID: fmt.Sprintf("%s-%d", t.Name(), i), Function: schema.FunctionCall{Name: n}}
	}
	barrierRegistry.plan(ctx, calls)
	return calls
}

func slotCount(ctx context.Context, calls []schema.ToolCall) int {
	barrierRegistry.mu.Lock()
	defer barrierRegistry.mu.Unlock()
	n := 0
	for _, c := range calls {
		if _, ok := barrierRegistry.slots[barrierKey(ctx, c.ID)]; ok {
			n++
		}
	}
	return n
}

func TestBarrierWithoutAUsablePlanChangesNothing(t *testing.T) {
	declareProbeTools(t)
	ctx := BindRunContext(context.Background(), t.Name())

	dup := []schema.ToolCall{
		{ID: "same", Function: schema.FunctionCall{Name: "bar_write"}},
		{ID: "same", Function: schema.FunctionCall{Name: "bar_shell"}},
	}
	barrierRegistry.plan(ctx, dup)
	if slotCount(ctx, dup) != 0 {
		t.Fatal("repeated call IDs were planned")
	}
	empty := []schema.ToolCall{
		{ID: "", Function: schema.FunctionCall{Name: "bar_write"}},
		{ID: "x", Function: schema.FunctionCall{Name: "bar_shell"}},
	}
	barrierRegistry.plan(ctx, empty)
	if slotCount(ctx, empty) != 0 {
		t.Fatal("an empty call ID was planned")
	}
	reads := planFor(t, ctx, "bar_read_a", "bar_read_b")
	if slotCount(ctx, reads) != 0 {
		t.Fatal("a batch of only read-only calls was planned")
	}

	// And a call with no plan is not made to wait.
	finish, err := gate(ctx, "never-planned")
	if err != nil {
		t.Fatal(err)
	}
	finish()
}

func TestBarrierWaitEndsWhenTheContextEnds(t *testing.T) {
	declareProbeTools(t)
	base := BindRunContext(context.Background(), t.Name())
	calls := planFor(t, base, "bar_write", "bar_shell") // the write never runs
	ctx, cancel := context.WithCancel(base)
	result := make(chan error, 1)
	go func() {
		finish, err := gate(ctx, calls[1].ID)
		finish()
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("returned before the earlier call finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not end when the context was cancelled")
	}
	barrierRegistry.take(barrierKey(base, calls[0].ID))
}

func TestBarrierRerunOfACallIsNotHeldUp(t *testing.T) {
	declareProbeTools(t)
	ctx := BindRunContext(context.Background(), t.Name())
	calls := planFor(t, ctx, "bar_write", "bar_shell")
	released := make(chan struct{})
	go func() {
		finish, _ := gate(ctx, calls[1].ID)
		finish()
		close(released)
	}()
	select {
	case <-released:
		t.Fatal("did not wait for the earlier call")
	case <-time.After(80 * time.Millisecond):
	}
	first, _ := gate(ctx, calls[0].ID)
	first()
	<-released
	if n := slotCount(ctx, calls); n != 0 {
		t.Fatalf("%d finished calls were left in the registry", n)
	}
	// The same call again (an approval rerun) finds no slot and does not wait.
	again, err := gate(ctx, calls[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestBarrierDoesNotMixUpTwoSessionsUsingTheSameCallIDs(t *testing.T) {
	declareProbeTools(t)
	a := BindRunContext(context.Background(), t.Name()+"-a")
	b := BindRunContext(context.Background(), t.Name()+"-b")
	calls := planFor(t, a, "bar_write", "bar_shell") // session a only
	done := make(chan struct{})
	go func() {
		finish, _ := gate(b, calls[1].ID) // same ID, other session
		finish()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session b waited on session a's calls")
	}
	for _, c := range calls {
		barrierRegistry.take(barrierKey(a, c.ID))
	}
}

// A streaming-only tool never goes through the plain wrapper. If its slot did
// not finish, every later call of the reply would wait for it.
func TestBarrierStreamingFormsAlsoFinishTheirSlot(t *testing.T) {
	declareProbeTools(t)
	m := &toolBarrierMiddleware{}
	forms := map[string]func(ctx context.Context, id string) error{
		"stream": func(ctx context.Context, id string) error {
			w, _ := m.WrapStreamableToolCall(ctx, func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
				return schema.StreamReaderFromArray([]string{"x"}), nil
			}, &adk.ToolContext{Name: "bar_write", CallID: id})
			_, err := w(ctx, "{}")
			return err
		},
		"enhanced": func(ctx context.Context, id string) error {
			w, _ := m.WrapEnhancedInvokableToolCall(ctx, func(context.Context, *schema.ToolArgument, ...tool.Option) (*schema.ToolResult, error) {
				return &schema.ToolResult{}, nil
			}, &adk.ToolContext{Name: "bar_write", CallID: id})
			_, err := w(ctx, &schema.ToolArgument{})
			return err
		},
		"enhanced-stream": func(ctx context.Context, id string) error {
			w, _ := m.WrapEnhancedStreamableToolCall(ctx, func(context.Context, *schema.ToolArgument, ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
				return schema.StreamReaderFromArray([]*schema.ToolResult{{}}), nil
			}, &adk.ToolContext{Name: "bar_write", CallID: id})
			_, err := w(ctx, &schema.ToolArgument{})
			return err
		},
	}
	for name, run := range forms {
		t.Run(name, func(t *testing.T) {
			ctx := BindRunContext(context.Background(), t.Name())
			calls := planFor(t, ctx, "bar_write", "bar_shell")
			released := make(chan struct{})
			go func() {
				finish, _ := gate(ctx, calls[1].ID)
				finish()
				close(released)
			}()
			select {
			case <-released:
				t.Fatal("later call did not wait for the earlier one")
			case <-time.After(80 * time.Millisecond):
			}
			if err := run(ctx, calls[0].ID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("later call still waiting after the earlier one returned")
			}
		})
	}
}

func TestToolExecutionHandlersPutTheBarrierFirstAmongCallWrappers(t *testing.T) {
	r := newPipelineTestRunner(t)
	h := r.toolExecutionHandlers(context.Background())
	if len(h.calls) == 0 {
		t.Fatal("no call handlers")
	}
	if _, ok := h.calls[0].(*toolBarrierMiddleware); !ok {
		t.Fatalf("first call handler is %T, want the barrier (it must wait before approval prompts)", h.calls[0])
	}
	t.Setenv("AGENTGO_TOOL_BARRIER", "0")
	for _, c := range r.toolExecutionHandlers(context.Background()).calls {
		if _, ok := c.(*toolBarrierMiddleware); ok {
			t.Fatal("barrier still installed with AGENTGO_TOOL_BARRIER=0")
		}
	}
}
