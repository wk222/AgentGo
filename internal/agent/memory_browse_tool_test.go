package agent

import (
	"agentgo/internal/memory"
	"agentgo/internal/promptbudget"
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"strings"
	"sync"
	"testing"
)

func TestMemoryBrowseConcurrentBudgetScopeAndOriginalPaging(t *testing.T) {
	ctx := context.Background()
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	text := strings.Repeat("中文猫", 250)
	for _, r := range []memory.Record{
		{ID: "source", Scope: "s", Modality: "fact", Status: "archived", Content: text},
		{ID: "active", Scope: "s", Modality: "fact", Status: "active", Content: "needle active", Metadata: map[string]any{"source_ids": []string{"source"}}},
		{ID: "secret", Scope: "other", Modality: "fact", Status: "active", Content: "SECRET"},
	} {
		if err = s.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	engine := memory.NewPipeline(s)
	b := &memoryReadBudget{id: "run", total: 1100, automatic: 200}
	reader, err := newMemoryBrowseTool(s, engine, "s", b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.InvokableRun(ctx, `{"action":"read","record_id":"secret"}`); err == nil {
		t.Fatal("cross-scope read")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	spent := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, e := reader.InvokableRun(ctx, `{"action":"read","record_id":"source","max_chars":200}`)
			if e != nil {
				t.Error(e)
				return
			}
			var r memoryBrowseResult
			if e = json.Unmarshal([]byte(raw), &r); e != nil {
				t.Error(e)
				return
			}
			if r.Generation != "" {
				mu.Lock()
				spent += promptbudget.Estimate(raw)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if spent != b.toolTokens || spent+b.automatic > b.total || spent == 0 {
		t.Fatal(spent, b)
	}
	// A fresh run can reopen archived evidence in exact Unicode pages.
	all := ""
	offset := 0
	for i := 0; i < 10; i++ {
		nextBudget := &memoryReadBudget{id: "page", total: 4000}
		r, _ := newMemoryBrowseTool(s, engine, "s", nextBudget)
		args, _ := json.Marshal(memoryBrowseInput{Action: "read", RecordID: "source", Offset: offset, MaxChars: 100})
		raw, e := r.InvokableRun(ctx, string(args))
		if e != nil {
			t.Fatal(e)
		}
		var page memoryBrowseResult
		if e = json.Unmarshal([]byte(raw), &page); e != nil || len(page.Items) != 1 {
			t.Fatal(raw, e)
		}
		all += page.Items[0].Text
		if page.NextOffset == 0 {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("paging stalled")
		}
		offset = page.NextOffset
	}
	if all != text {
		t.Fatal("lost original text", len([]rune(all)))
	}
	searchBudget := &memoryReadBudget{id: "search", total: 4000}
	search, _ := newMemoryBrowseTool(s, engine, "s", searchBudget)
	raw, e := search.InvokableRun(ctx, `{"action":"search","query":"needle"}`)
	if e != nil || !strings.Contains(raw, "needle active") || strings.Contains(raw, "SECRET") {
		t.Fatal(raw, e)
	}
}
func TestMemoryReadsPrunePreviousGenerationWithoutMutatingHistory(t *testing.T) {
	original := schema.ToolMessage(encodeMemoryResult(memoryBrowseResult{Generation: "old|s:1:0", Items: []memoryBrowseItem{{ID: "a", Text: "STALE"}}}), "c")
	original.Name = "browse_memory"
	current := schema.ToolMessage(encodeMemoryResult(memoryBrowseResult{Generation: "new|s:2:0", Items: []memoryBrowseItem{{ID: "b", Text: "CURRENT"}}}), "d")
	current.Name = "browse_memory"
	other := schema.ToolMessage("UNRELATED", "e")
	messages := []*schema.Message{original, current, other}
	out, changed := pruneOlderMemoryReads(messages, "new|s:2:0")
	if !changed || strings.Contains(out[0].Content, "STALE") || !strings.Contains(original.Content, "STALE") || out[1] != current || out[2] != other {
		t.Fatal(out)
	}
	if _, changed = pruneOlderMemoryReads(out, "new|s:2:0"); changed {
		t.Fatal("pruning is not idempotent")
	}
	// A correction in the same run also invalidates prior evidence.
	if out, changed = pruneOlderMemoryReads(messages, "new|s:3:0"); !changed || strings.Contains(out[1].Content, "CURRENT") {
		t.Fatal(out)
	}
}
func TestMemoryBeforeAgentInstallsScopedToolAndSharedBudget(t *testing.T) {
	t.Setenv("AGENTGO_MEMORY_TOKENS", "400")
	s, e := memory.NewSQLiteStore(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB().Close()
	m := NewMemoryMiddleware(memory.NewPipeline(s))
	ctx := BindRunContext(context.Background(), "s")
	before := &adk.ChatModelAgentContext{}
	ctx, after, e := m.BeforeAgent(ctx, before)
	if e != nil || len(after.Tools) != 1 || len(before.Tools) != 0 || memoryBudgetFrom(ctx) == nil {
		t.Fatal(after, e)
	}
	info, e := after.Tools[0].Info(ctx)
	if e != nil || info.Name != "browse_memory" {
		t.Fatal(info, e)
	}
	budget := memoryBudgetFrom(ctx)
	ctx, _, e = m.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("hello")}}, nil)
	if e != nil || memoryBudgetFrom(ctx) != budget || budget.automatic+budget.toolTokens > budget.total {
		t.Fatal(budget, e)
	}
}

type progressiveMemoryModel struct {
	sawTool     bool
	sawEvidence bool
}

func (m *progressiveMemoryModel) Generate(_ context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	for _, info := range model.GetCommonOptions(nil, opts...).Tools {
		if info.Name == "browse_memory" {
			m.sawTool = true
		}
	}
	for _, msg := range messages {
		if msg.Role == schema.Tool && strings.Contains(msg.Content, "remember-evidence") {
			m.sawEvidence = true
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "read-source", Type: "function", Function: schema.FunctionCall{Name: "browse_memory", Arguments: `{"action":"read","record_id":"evidence"}`}}}), nil
}
func (m *progressiveMemoryModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func (m *progressiveMemoryModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	for _, info := range infos {
		if info.Name == "browse_memory" {
			m.sawTool = true
		}
	}
	return m, nil
}
func TestRealADKChainUsesScopedProgressiveMemory(t *testing.T) {
	t.Setenv("AGENTGO_MEMORY_TOKENS", "2000")
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	if err = s.Ingest(context.Background(), memory.Record{ID: "evidence", Scope: "s", Modality: "fact", Content: "remember-evidence", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	r := newPipelineTestRunner(t)
	r.memMW = NewMemoryMiddleware(memory.NewPipeline(s))
	ctx := BindRunContext(context.Background(), "s")
	model := &progressiveMemoryModel{}
	handlers, err := r.BuildADKHandlers(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "progressive", Description: "test", Model: model, Handlers: handlers, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a})
	it := runner.Run(ctx, []adk.Message{schema.UserMessage("Read the evidence")})
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
	if !model.sawTool || !model.sawEvidence {
		t.Fatal("dynamic memory tool was not mounted or its output was lost", model)
	}
}
func TestAgenticMemoryRefreshDoesNotAppendRepeatedly(t *testing.T) {
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := memory.NewPipeline(s)
	p.SetContextPromptHook(func(context.Context, string) (string, error) { return "bounded reference", nil })
	m := NewAgenticMemoryMiddleware(p)
	original := schema.SystemAgenticMessage("BASE")
	originalText := original.String()
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{original, schema.UserAgenticMessage("hello")}}
	ctx := BindRunContext(context.Background(), "s")
	for i := 0; i < 20; i++ {
		var err error
		ctx, state, err = m.BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(state.Messages) != 3 || original.String() != originalText {
		t.Fatal("repeated injection or archived system was changed", len(state.Messages), original)
	}
	p.SetContextPromptHook(func(context.Context, string) (string, error) { return "", nil })
	_, state, err = m.BeforeModelRewriteState(ctx, state, nil)
	if err != nil || len(state.Messages) != 2 {
		t.Fatal("stale injected memory survived refresh", state, err)
	}
}
