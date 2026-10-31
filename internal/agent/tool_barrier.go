package agent

import (
	"context"
	"sync"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/governance"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// The framework runs every tool call of one model reply at the same time, and
// it starts them last-to-first: the first call runs on the calling goroutine
// after all the others have been launched. A reply of [write file, run script]
// therefore started the script before the file was written, every time.
//
// This keeps the concurrency that is safe and orders the rest. Calls that are
// declared read-only still run together. Any call that changes files, or that
// declared nothing about itself, waits for every earlier call of the reply to
// finish, and every later call waits for it. That is the order the model listed
// them in, which is the order it assumed.
//
// Nothing here can hold a call forever: waits are only on earlier calls of the
// same reply, they end when the context ends, and a call that is interrupted for
// approval or fails still counts as finished. Any missing information (no plan,
// repeated or empty call IDs, a resumed call) leaves the call to run as before.

type barrierSlot struct {
	barrier bool
	done    chan struct{}
	once    sync.Once
	after   []*barrierSlot // earlier calls of the reply this one must wait for
	born    time.Time
}

func (s *barrierSlot) finish() { s.once.Do(func() { close(s.done) }) }

type barrierPlans struct {
	mu    sync.Mutex
	slots map[string]*barrierSlot
}

var barrierRegistry = &barrierPlans{slots: map[string]*barrierSlot{}}

func barrierKey(ctx context.Context, callID string) string {
	return SessionIDFromContext(ctx) + "\x00" + callID
}

// parallelSafeTool is true only for a tool that declared itself read-only and
// low risk. An undeclared tool is treated as one that may change anything.
func parallelSafeTool(name string) bool {
	t, ok := governance.ToolTraitsOf(name)
	return ok && !t.Mutating && (t.Risk == "" || t.Risk == governance.RiskLow)
}

func (p *barrierPlans) plan(ctx context.Context, calls []schema.ToolCall) {
	slots := make([]*barrierSlot, len(calls))
	seen := make(map[string]bool, len(calls))
	anyBarrier := false
	for i, c := range calls {
		if c.ID == "" || seen[c.ID] {
			return
		}
		seen[c.ID] = true
		slots[i] = &barrierSlot{barrier: !parallelSafeTool(c.Function.Name), done: make(chan struct{}), born: time.Now()}
		anyBarrier = anyBarrier || slots[i].barrier
	}
	if !anyBarrier {
		return // all read-only: nothing to order
	}
	for k := range slots {
		for i := 0; i < k; i++ {
			if slots[k].barrier || slots[i].barrier {
				slots[k].after = append(slots[k].after, slots[i])
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for key, s := range p.slots {
		if s.born.Before(cutoff) { // a plan whose calls never ran
			delete(p.slots, key)
			s.finish()
		}
	}
	for i, c := range calls {
		p.slots[barrierKey(ctx, c.ID)] = slots[i]
	}
}

// take removes and returns the call's slot, so a rerun of the same call after an
// interruption finds none and runs as it would without a plan.
func (p *barrierPlans) take(key string) *barrierSlot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.slots[key]
	delete(p.slots, key)
	return s
}

type toolBarrierMiddleware struct {
	adk.BaseChatModelAgentMiddleware
}

// NewToolBarrierMiddleware orders a reply's file-changing and undeclared tool
// calls; it returns nil when AGENTGO_TOOL_BARRIER=0.
func NewToolBarrierMiddleware() adk.ChatModelAgentMiddleware {
	if !config.ToolBarrier.On() {
		return nil
	}
	return &toolBarrierMiddleware{}
}

func (m *toolBarrierMiddleware) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state != nil && len(state.Messages) > 0 {
		if last := state.Messages[len(state.Messages)-1]; last != nil && last.Role == schema.Assistant && len(last.ToolCalls) > 1 {
			barrierRegistry.plan(ctx, last.ToolCalls)
		}
	}
	return ctx, state, nil
}

// gate waits for the earlier calls this call must follow. The returned func
// marks this call finished and is always safe to call; without a plan for the
// call it does nothing and nothing is waited for.
func gate(ctx context.Context, callID string) (finish func(), err error) {
	slot := barrierRegistry.take(barrierKey(ctx, callID))
	if slot == nil {
		return func() {}, nil
	}
	for _, earlier := range slot.after {
		select {
		case <-earlier.done:
		case <-ctx.Done():
			slot.finish()
			return func() {}, ctx.Err()
		}
	}
	return slot.finish, nil
}

func (m *toolBarrierMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tc *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	if tc == nil || tc.CallID == "" {
		return endpoint, nil
	}
	callID := tc.CallID
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		finish, err := gate(ctx, callID)
		if err != nil {
			return "", err
		}
		defer finish()
		return endpoint(ctx, args, opts...)
	}, nil
}

// The streaming forms must take part too: a streaming-only tool never reaches
// WrapInvokableToolCall, so its slot would never finish and a later call would
// wait on it. Such a call counts as finished once its stream is handed back.
func (m *toolBarrierMiddleware) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, tc *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	if tc == nil || tc.CallID == "" {
		return endpoint, nil
	}
	callID := tc.CallID
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		finish, err := gate(ctx, callID)
		if err != nil {
			return nil, err
		}
		defer finish()
		return endpoint(ctx, args, opts...)
	}, nil
}

func (m *toolBarrierMiddleware) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, tc *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	if tc == nil || tc.CallID == "" {
		return endpoint, nil
	}
	callID := tc.CallID
	return func(ctx context.Context, arg *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		finish, err := gate(ctx, callID)
		if err != nil {
			return nil, err
		}
		defer finish()
		return endpoint(ctx, arg, opts...)
	}, nil
}

func (m *toolBarrierMiddleware) WrapEnhancedStreamableToolCall(_ context.Context, endpoint adk.EnhancedStreamableToolCallEndpoint, tc *adk.ToolContext) (adk.EnhancedStreamableToolCallEndpoint, error) {
	if tc == nil || tc.CallID == "" {
		return endpoint, nil
	}
	callID := tc.CallID
	return func(ctx context.Context, arg *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		finish, err := gate(ctx, callID)
		if err != nil {
			return nil, err
		}
		defer finish()
		return endpoint(ctx, arg, opts...)
	}, nil
}
