package agent

import (
	"agentgo/internal/memory"
	"agentgo/internal/sessions"
	"agentgo/internal/workspace"
	"context"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type compactionTestModel struct {
	calls   int
	failure error
}

func (m *compactionTestModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	return nil, m.failure
}
func (m *compactionTestModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not used")
}
func TestContextSummaryThresholdFitsCanvas(t *testing.T) {
	for _, c := range []ExecutionCanvas{CanvasFocused, CanvasBalanced, CanvasDeep} {
		p := CanvasPolicyFor(c)
		cfg := contextSummaryConfig[*schema.Message](nil, p)
		if cfg.Trigger.ContextTokens >= p.ContextTokenBudget {
			t.Fatal("summary triggers after budget", c)
		}
		if !strings.Contains(cfg.UserInstruction, "未验证") {
			t.Fatal("summary prompt lacks verification status")
		}
	}
}
func TestFailedSummaryKeepsConversationAndBacksOff(t *testing.T) {
	model := &compactionTestModel{failure: errors.New("temporary upstream failure")}
	cfg := contextSummaryConfig[*schema.Message](model, CanvasPolicy{SummarizationTokens: 500, ContextTokenBudget: 1000})
	mw, err := summarization.NewTyped(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &resilientSummary[*schema.Message]{TypedChatModelAgentMiddleware: mw, timeout: time.Second}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("base"), schema.UserMessage(strings.Repeat("history ", 1500)), schema.AssistantMessage("ok", nil), schema.UserMessage("current request")}}
	for i := 0; i < 2; i++ {
		ctx, out, err := wrapper.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil || out != state || ctx.Err() != nil {
			t.Fatal("summary failure broke model request", err)
		}
	}
	if model.calls != 1 {
		t.Fatalf("retried failed summary each round: %d", model.calls)
	}
}
func TestContextSummaryKeepsCurrentRequestAndToolPairs(t *testing.T) {
	original := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("old request"), schema.AssistantMessage("done", nil), schema.UserMessage("LATEST: preserve this request"), schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "verify"}}}), schema.ToolMessage("verified", "c")}
	out, err := finalizeContextSummary(original, schema.AssistantMessage("目标和约束；已验证；待办", nil))
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range out {
		joined += m.Content
	}
	if !strings.Contains(joined, "LATEST") || !strings.Contains(joined, "verified") {
		t.Fatal("summary lost current request or result")
	}
	if _, err := finalizeContextSummary(original, schema.AssistantMessage("", nil)); err == nil {
		t.Fatal("accepted empty summary")
	}
	v := sessions.CompileProjectedView(out, sessions.CompileOptions{})
	if len(v.ActiveTurn) < 3 {
		t.Fatal("lost paired tail")
	}
}
func TestWorkspaceRulesInjectedOnceAcrossSpineRounds(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "TEAM.md"), []byte("UNIQUE-WORKSPACE-RULE"), 0600)
	ws := workspace.NewContextMiddleware(root)
	spine := NewSessionSpineMiddleware(SessionSpineConfig{WorkspaceRoot: root})
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("hello")}}
	for i := 0; i < 8; i++ {
		ctx, _, err := ws.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, state, err = spine.BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(state.Messages[0].Content, "## Team Context") != 1 || strings.Count(state.Messages[0].Content, "<system_rules>") != 1 {
		t.Fatal("workspace rules accumulated")
	}
}
func TestContextBudgetCountsArgumentsSchemasAndImages(t *testing.T) {
	v := &sessions.ProjectedRuntimeView{ActiveTurn: []sessions.FlatMessage{{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "tool", Arguments: strings.Repeat("x", 9000)}}}}, {Role: "user", UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL}}}}}
	r := InspectContextBudget(context.Background(), "s", v, 100, []*schema.ToolInfo{{Name: "tool", Desc: strings.Repeat("description ", 1000)}})
	if r.TotalTokens < 5000 {
		t.Fatalf("undercounted context %d", r.TotalTokens)
	}
	for _, d := range r.TruncationLog {
		if d.Removed != 0 {
			t.Fatal("inspection falsely claimed truncation")
		}
	}
}

func TestBudgetGateBoundsRecentErrorToolOutputWithoutDroppingRequest(t *testing.T) {
	mw := NewSessionSpineMiddleware(SessionSpineConfig{WorkspaceRoot: t.TempDir()})
	request := "当前要求：不要丢掉这个问题"
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage(request), schema.AssistantMessage("", []schema.ToolCall{{ID: "e", Function: schema.FunctionCall{Name: "check"}}}), schema.ToolMessage("error: HEAD "+strings.Repeat("diagnostic ", 30000)+" TAIL", "e")}}
	ctx := WithCanvasPolicy(context.Background(), CanvasPolicy{ContextTokenBudget: 4000})
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range out.Messages {
		if m.Role == schema.User && m.Content == request {
			found = true
		}
		if m.Role == schema.Tool && (len(m.Content) > 4300 || !strings.Contains(m.Content, "HEAD") || !strings.Contains(m.Content, "TAIL")) {
			t.Fatal("unbounded or lost error excerpt")
		}
	}
	if !found {
		t.Fatal("lost latest request")
	}
}
func TestAttachmentMetadataDetectionDoesNotSkipBracketedUserText(t *testing.T) {
	if !needsAttachmentMetadata([]*schema.Message{schema.UserMessage("[用户备注] 看一下截图")}, "s") {
		t.Fatal("treated user text as system injection")
	}
}

func TestChineseContextEstimateIsNotDividedByThree(t *testing.T) {
	if estimateTokens(strings.Repeat("中文", 1000)) < 2000 {
		t.Fatal("Chinese context significantly undercounted")
	}
}

func TestSemanticSummaryAlsoKeepsDeterministicConstraintLedger(t *testing.T) {
	original := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("original-goal-do-not-touch-eino"), schema.UserMessage("user-correction-keep-tests")}
	for i := 0; i < 20; i++ {
		original = append(original, schema.AssistantMessage("old progress", nil))
	}
	original = append(original, schema.UserMessage("latest request"))
	out, err := finalizeContextSummary(original, schema.AssistantMessage("short summary omitted details", nil))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, m := range out {
		all += m.Content
	}
	for _, want := range []string{"original-goal-do-not-touch-eino", "user-correction-keep-tests", "latest request"} {
		if !strings.Contains(all, want) {
			t.Fatalf("semantic summary lost deterministic anchor %s", want)
		}
	}
}

type contextRecallEngine struct {
	memory.Engine
	calls  int
	prompt string
}

func (e *contextRecallEngine) ContextPrompt(ctx context.Context, scope string) (string, error) {
	e.calls++
	return e.prompt + scope, nil
}
func TestMemoryRecallCacheAndWorkspaceInjectionStaySeparate(t *testing.T) {
	e := &contextRecallEngine{prompt: "RECALLED-FACT-"}
	mem := NewMemoryMiddleware(e)
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "TEAM.md"), []byte("WORKSPACE-RULE"), 0600)
	ws := workspace.NewContextMiddleware(root)
	spine := NewSessionSpineMiddleware(SessionSpineConfig{WorkspaceRoot: root})
	ctx := WithSessionID(context.Background(), "scope-a")
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("hello")}}
	for i := 0; i < 5; i++ {
		next, _, _ := ws.BeforeModelRewriteState(ctx, state, nil)
		_, _, _ = mem.BeforeModelRewriteState(next, state, nil)
		_, state, _ = spine.BeforeModelRewriteState(next, state, nil)
	}
	if e.calls != 1 {
		t.Fatalf("repeated memory retrieval: %d", e.calls)
	}
	if !strings.Contains(contextMessagesText(state.Messages), "RECALLED-FACT-scope-a") || strings.Count(state.Messages[0].Content, "WORKSPACE-RULE") != 1 {
		t.Fatal("memory lost or workspace duplicated")
	}
	mem.cacheMu.Lock()
	delete(mem.recallCache, "scope-a")
	mem.cacheMu.Unlock()
	e.prompt = "UPDATED-FACT-"
	_, _, _ = mem.BeforeModelRewriteState(ctx, state, nil)
	_, state, _ = spine.BeforeModelRewriteState(ctx, state, nil)
	if strings.Contains(contextMessagesText(state.Messages), "RECALLED-FACT") || !strings.Contains(contextMessagesText(state.Messages), "UPDATED-FACT") {
		t.Fatal("stale recalled facts accumulated")
	}
}

func TestSemanticSummaryActuallyReducesFewLargeOldMessages(t *testing.T) {
	old := strings.Repeat("old pasted source code ", 3000)
	original := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage(old), schema.AssistantMessage("checked", nil), schema.UserMessage("LATEST follow-up request")}
	out, err := finalizeContextSummary(original, schema.AssistantMessage("目标：检查代码；验证结果：已检查；待办：继续修复", nil))
	if err != nil {
		t.Fatal(err)
	}
	before, after := 0, 0
	for _, m := range original {
		before += messageContextTokens(m)
	}
	for _, m := range out {
		after += messageContextTokens(m)
		if m.Role == schema.User && m.Content == old {
			t.Fatal("kept old giant user message verbatim")
		}
	}
	if after >= before/2 {
		t.Fatalf("summary did not reduce large old history: %d to %d", before, after)
	}
}

type successfulCompactionModel struct{ compactionTestModel }

func (m *successfulCompactionModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	return schema.AssistantMessage("目标与约束：检查旧代码。已确认：完成检查。待办：继续修复。", nil), nil
}
func TestSemanticSummaryMiddlewareUsesConfiguredFinalizer(t *testing.T) {
	m := &successfulCompactionModel{}
	cfg := contextSummaryConfig[*schema.Message](m, CanvasPolicy{SummarizationTokens: 500, ContextTokenBudget: 1000})
	mw, err := summarization.NewTyped(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &resilientSummary[*schema.Message]{TypedChatModelAgentMiddleware: mw, timeout: time.Second}
	old := strings.Repeat("old code ", 5000)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage(old), schema.AssistantMessage("checked", nil), schema.UserMessage("LATEST follow-up")}}
	ctx, out, err := wrapper.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil || ctx.Err() != nil || m.calls != 1 {
		t.Fatal("summary not executed", err, m.calls)
	}
	foundSummary, foundUser := false, false
	for _, msg := range out.Messages {
		if strings.HasPrefix(msg.Content, "[System Summary]") {
			foundSummary = true
		}
		if msg.Content == "LATEST follow-up" {
			foundUser = true
		}
		if msg.Role == schema.User && msg.Content == old {
			t.Fatal("old context was not compacted")
		}
	}
	if !foundSummary || !foundUser {
		t.Fatal("summary or latest request lost")
	}
}

func contextMessagesText(msgs []*schema.Message) string {
	var out []string
	for _, m := range msgs {
		if m != nil {
			out = append(out, m.Content)
		}
	}
	return strings.Join(out, "\n")
}

func TestSummaryCancellationPropagatesWithoutCooldown(t *testing.T) {
	m := &compactionTestModel{failure: context.Canceled}
	cfg := contextSummaryConfig[*schema.Message](m, CanvasPolicy{SummarizationTokens: 500, ContextTokenBudget: 1000})
	mw, err := summarization.NewTyped(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &resilientSummary[*schema.Message]{TypedChatModelAgentMiddleware: mw, timeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("short")}}
	_, _, err = wrapper.BeforeModelRewriteState(ctx, state, nil)
	if !errors.Is(err, context.Canceled) || m.calls != 0 || !wrapper.next.IsZero() {
		t.Fatalf("cancel swallowed or triggered work: %v calls=%d", err, m.calls)
	}
}
func TestSummaryRejectsTokenExpansionBeforeReplacingHistory(t *testing.T) {
	cfg := contextSummaryConfig[*schema.Message](nil, CanvasPolicy{SummarizationTokens: 500, ContextTokenBudget: 1000})
	original := []*schema.Message{schema.UserMessage("old request"), schema.AssistantMessage("done", nil), schema.UserMessage("LATEST")}
	_, err := cfg.Finalize(context.Background(), original, schema.AssistantMessage(strings.Repeat("expanding summary ", 2000), nil))
	if !errors.Is(err, errSummaryNoReduction) {
		t.Fatalf("accepted expanding replacement: %v", err)
	}
}

type cancelDuringSummaryModel struct {
	compactionTestModel
	cancel context.CancelFunc
}

func (m *cancelDuringSummaryModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	m.cancel()
	return nil, context.Canceled
}
func TestSummaryCancellationDuringModelCallDoesNotBecomeFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancelDuringSummaryModel{cancel: cancel}
	mw, err := summarization.NewTyped(ctx, contextSummaryConfig[*schema.Message](m, CanvasPolicy{SummarizationTokens: 500, ContextTokenBudget: 1000}))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &resilientSummary[*schema.Message]{TypedChatModelAgentMiddleware: mw, timeout: time.Second}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("base"), schema.UserMessage(strings.Repeat("old ", 6000)), schema.AssistantMessage("old answer", nil), schema.UserMessage("latest")}}
	_, out, err := wrapper.BeforeModelRewriteState(ctx, state, nil)
	if !errors.Is(err, context.Canceled) || out != state || m.calls == 0 || !wrapper.next.IsZero() {
		t.Fatalf("cancel became fallback: err=%v calls=%d cooldown=%v", err, m.calls, wrapper.next)
	}
}
