package agent

import (
	"context"
	"testing"
	"time"
)

func TestCanvasPolicyPresets(t *testing.T) {
	focused := CanvasPolicyFor(CanvasFocused)
	if focused.MaxIterations != 6 {
		t.Errorf("expected MaxIterations = 6 for focused, got %d", focused.MaxIterations)
	}
	if focused.LLMTimeout != 30*time.Second {
		t.Errorf("expected LLMTimeout = 30s, got %v", focused.LLMTimeout)
	}

	balanced := CanvasPolicyFor(CanvasBalanced)
	if balanced.MaxIterations != 12 {
		t.Errorf("expected MaxIterations = 12 for balanced, got %d", balanced.MaxIterations)
	}

	deep := CanvasPolicyFor(CanvasDeep)
	if deep.MaxIterations != 20 {
		t.Errorf("expected MaxIterations = 20 for deep, got %d", deep.MaxIterations)
	}
	if deep.AgentDepth != 4 {
		t.Errorf("expected AgentDepth = 4, got %d", deep.AgentDepth)
	}
}

func TestContextCanvasPolicy(t *testing.T) {
	ctx := context.Background()

	// Default fallback
	pDefault := CanvasPolicyFromContext(ctx)
	if pDefault.MaxIterations != 12 {
		t.Errorf("expected default MaxIterations = 12, got %d", pDefault.MaxIterations)
	}

	// Manual injection
	custom := CanvasPolicy{MaxIterations: 42, AgentDepth: 99}
	ctxCustom := WithCanvasPolicy(ctx, custom)

	pCustom := CanvasPolicyFromContext(ctxCustom)
	if pCustom.MaxIterations != 42 || pCustom.AgentDepth != 99 {
		t.Errorf("expected custom policy values 42 and 99, got %d and %d", pCustom.MaxIterations, pCustom.AgentDepth)
	}

	// Mode-based fallback
	modeCtx := WithSessionMode(ctx, SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})
	pMode := CanvasPolicyFromContext(modeCtx)
	if pMode.MaxIterations != 20 || pMode.AgentDepth != 4 {
		t.Errorf("expected deep policy from session mode, got iterations=%d, depth=%d", pMode.MaxIterations, pMode.AgentDepth)
	}
}
