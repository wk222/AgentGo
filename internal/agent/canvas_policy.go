package agent

import (
	"context"
	"time"
)

// ExecutionCanvas mirrors PyBot focused | balanced | deep.
type ExecutionCanvas string

const (
	CanvasFocused  ExecutionCanvas = "focused"
	CanvasBalanced ExecutionCanvas = "balanced"
	CanvasDeep     ExecutionCanvas = "deep"
)

// CanvasPolicy encapsulates all runtime strategies and resource thresholds
// associated with an ExecutionCanvas, unifying limits across LLMs, tools, memory, and agents.
type CanvasPolicy struct {
	// Eino Middleware thresholds
	SummarizationTokens  int
	ReductionClearTokens int
	MaxIterations        int
	DistillIntervalHours int

	// Timeout and budget strategies
	LLMTimeout           time.Duration
	ToolBudgetPerSession float64
	MaxConcurrentTools   int
	StuckLoopThreshold   int
	AgentDepth           int
	MemoryRecallTopK     int
	ContextTokenBudget   int
}

type canvasPolicyCtxKey struct{}

// CanvasPolicies maps each ExecutionCanvas to its preset policy (Single Source of Truth).
var CanvasPolicies = map[ExecutionCanvas]CanvasPolicy{
	CanvasFocused: {
		SummarizationTokens:  60_000,
		ReductionClearTokens: 80_000,
		MaxIterations:        6,
		DistillIntervalHours: 48,
		LLMTimeout:           30 * time.Second,
		ToolBudgetPerSession: 5.0,
		MaxConcurrentTools:   1,
		StuckLoopThreshold:   3,
		AgentDepth:           1,
		MemoryRecallTopK:     3,
		ContextTokenBudget:   16_384,
	},
	CanvasBalanced: {
		SummarizationTokens:  120_000,
		ReductionClearTokens: 120_000,
		MaxIterations:        12,
		DistillIntervalHours: 24,
		LLMTimeout:           60 * time.Second,
		ToolBudgetPerSession: 20.0,
		MaxConcurrentTools:   3,
		StuckLoopThreshold:   5,
		AgentDepth:           2,
		MemoryRecallTopK:     5,
		ContextTokenBudget:   32_768,
	},
	CanvasDeep: {
		SummarizationTokens:  180_000,
		ReductionClearTokens: 160_000,
		MaxIterations:        20,
		DistillIntervalHours: 6,
		LLMTimeout:           120 * time.Second,
		ToolBudgetPerSession: 100.0,
		MaxConcurrentTools:   5,
		StuckLoopThreshold:   8,
		AgentDepth:           4,
		MemoryRecallTopK:     10,
		ContextTokenBudget:   65_536,
	},
}

// CanvasPolicyFor returns the policy associated with the given ExecutionCanvas.
func CanvasPolicyFor(c ExecutionCanvas) CanvasPolicy {
	normalized := CanvasBalanced
	if c == CanvasFocused || c == CanvasDeep {
		normalized = c
	}
	return CanvasPolicies[normalized]
}

// WithCanvasPolicy injects a CanvasPolicy into the context.
func WithCanvasPolicy(ctx context.Context, p CanvasPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, canvasPolicyCtxKey{}, p)
}

// CanvasPolicyFromContext extracts a CanvasPolicy from the context, falling back to SessionMode Canvas, then Balanced.
func CanvasPolicyFromContext(ctx context.Context) CanvasPolicy {
	if ctx == nil {
		return CanvasPolicyFor(CanvasBalanced)
	}
	if p, ok := ctx.Value(canvasPolicyCtxKey{}).(CanvasPolicy); ok {
		return p
	}
	sm := SessionModeFromContext(ctx)
	if sm.Canvas != "" {
		return CanvasPolicyFor(sm.Canvas)
	}
	return CanvasPolicyFor(CanvasBalanced)
}
