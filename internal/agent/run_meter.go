package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
)

// RunUsage is the per-run consumption snapshot reported to callers such as the
// gateway and the evaluation harness. Everything is counted for ONE run.
type RunUsage struct {
	ModelCalls       int `json:"model_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
	ToolCalls        int `json:"tool_calls"`
}

// CacheHitRate is cached / prompt tokens (0 when nothing was sent).
func (u RunUsage) CacheHitRate() float64 {
	if u.PromptTokens <= 0 {
		return 0
	}
	return float64(u.CachedTokens) / float64(u.PromptTokens)
}

// RunMeter accumulates RunUsage for one run. Bind it with WithRunMeter; the
// runner's model callback and the tool-call middleware feed it.
type RunMeter struct {
	mu      sync.Mutex
	u       RunUsage
	pending sync.WaitGroup // streamed model calls whose usage has not landed yet
}

type runMeterKey struct{}

// WithRunMeter binds m to the run carried by ctx.
func WithRunMeter(ctx context.Context, m *RunMeter) context.Context {
	if m == nil {
		return ctx
	}
	return context.WithValue(ctx, runMeterKey{}, m)
}

func runMeterFrom(ctx context.Context) *RunMeter {
	m, _ := ctx.Value(runMeterKey{}).(*RunMeter)
	return m
}

// runUsageHandler builds the always-on usage callback: every chat-model call is
// written to the ledger tracer (daily rollup) and, when the run carries one, to
// its RunMeter.
func (r *Runner) runUsageHandler(m *RunMeter) callbacks.Handler {
	var pending *sync.WaitGroup
	if m != nil {
		pending = &m.pending
	}
	return newUsageSinkHandler(func(modelName string, tu *model.TokenUsage) {
		if r.usageTracer != nil {
			r.usageTracer.Record(modelName, "", tu.PromptTokens, tu.PromptTokenDetails.CachedTokens,
				tu.CompletionTokens, tu.CompletionTokensDetails.ReasoningTokens, 0)
		}
		m.addModel(tu)
	}, pending)
}

func (m *RunMeter) addModel(tu *model.TokenUsage) {
	if m == nil || tu == nil {
		return
	}
	m.mu.Lock()
	m.u.ModelCalls++
	m.u.PromptTokens += tu.PromptTokens
	m.u.CachedTokens += tu.PromptTokenDetails.CachedTokens
	m.u.CompletionTokens += tu.CompletionTokens
	m.u.ReasoningTokens += tu.CompletionTokensDetails.ReasoningTokens
	m.mu.Unlock()
}

func (m *RunMeter) addToolCall() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.u.ToolCalls++
	m.mu.Unlock()
}

// Snapshot returns the totals so far. Call Wait first when the run just ended:
// streamed calls deliver their usage a moment after the stream is drained.
func (m *RunMeter) Snapshot() RunUsage {
	if m == nil {
		return RunUsage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.u
}

// Wait blocks until in-flight streamed usage has been recorded, or d elapses.
func (m *RunMeter) Wait(d time.Duration) {
	if m == nil {
		return
	}
	done := make(chan struct{})
	go func() { m.pending.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
