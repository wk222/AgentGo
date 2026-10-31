package agent

import (
	"agentgo/internal/sessions"
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestModelBudgetReservesOutputAndCapsPolicy(t *testing.T) {
	cfg := LLMSettings{ContextWindow: 8192, MaxOutputTokens: 2048}
	ctx := withLLMBudget(context.Background(), cfg)
	b := modelBudgetFrom(ctx)
	if b.Input != 5632 || b.Output != 2048 || CanvasPolicyFromContext(ctx).ContextTokenBudget != 5632 {
		t.Fatalf("unexpected budget: %+v", b)
	}
	if CanvasPolicyFromContext(withLLMBudget(ctx, cfg)) != CanvasPolicyFromContext(ctx) {
		t.Fatal("repeated budget injection drifts")
	}
	if modelBudgetFrom(withLLMBudget(context.Background(), LLMSettings{})).Input != 0 {
		t.Fatal("guessed unknown model window")
	}
}

func TestModelBudgetRejectsProtectedOversizeWithoutLosingRequest(t *testing.T) {
	request := strings.Repeat("当前要求", 2000)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage(request)}}
	ctx := withLLMBudget(BindRunContext(context.Background(), "budget-overflow"), LLMSettings{ContextWindow: 4096, MaxOutputTokens: 1024})
	mw := NewSessionSpineMiddleware(SessionSpineConfig{})
	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err == nil || !strings.Contains(err.Error(), "可用输入预算") {
		t.Fatalf("expected useful preflight error: %v", err)
	}
	if state.Messages[0].Content != request {
		t.Fatal("oversized current request silently truncated")
	}
	report, ok := GetContextBudgetReport("budget-overflow")
	if !ok || report.ReservedOutputTokens != 1024 || report.ModelContextWindow != 4096 {
		t.Fatal("missing model budget diagnostics")
	}
}

func TestAttachmentHydrationIncludesReservedPrefixUser(t *testing.T) {
	for _, text := range []string{"[System Summary] literal", "[Environment Feedback] literal"} {
		if !needsAttachmentMetadata([]*schema.Message{schema.UserMessage(text)}, "s") {
			t.Fatal("skipped real user attachment")
		}
	}
}

func TestMemoryTranscriptDoesNotTurnHostContextIntoUserSpeech(t *testing.T) {
	input := []*schema.Message{schema.UserMessage("real request"), sessions.ContextMessage("runtime", "HOST_REFERENCE_MARKER"), schema.AssistantMessage("real answer", nil)}
	text := transcriptFromMessages(input, 12)
	if strings.Contains(text, "HOST_REFERENCE_MARKER") || !strings.Contains(text, "real request") {
		t.Fatal("host context leaked into user journal")
	}
}

func TestModelBudgetDoesNotFreezeCanvasBeforeSessionBinding(t *testing.T) {
	ctx := withLLMBudget(context.Background(), LLMSettings{ContextWindow: 8192, MaxOutputTokens: 2048})
	ctx = WithSessionMode(ctx, SessionMode{Canvas: CanvasFocused})
	p := CanvasPolicyFromContext(ctx)
	if p.MaxIterations != 6 || p.LLMTimeout != CanvasPolicies[CanvasFocused].LLMTimeout || p.ContextTokenBudget != 5632 {
		t.Fatalf("model metadata replaced the selected canvas: %+v", p)
	}
}

func TestUnknownModelStillHonorsConfiguredContextBudget(t *testing.T) {
	request := strings.Repeat("protected request ", 10000)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage(request)}}
	ctx := WithSessionMode(context.Background(), SessionMode{Canvas: CanvasFocused})
	mw := NewSessionSpineMiddleware(SessionSpineConfig{})
	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err == nil || !strings.Contains(err.Error(), "configured input budget") {
		t.Fatal("unknown model bypassed context budget", err)
	}
	if state.Messages[0].Content != request {
		t.Fatal("current request was silently truncated")
	}
}
