package agent

import (
	"agentgo/internal/memory"
	"agentgo/internal/promptbudget"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestMemoryResidentBudgetReclaimsSlotsWithinSameRun(t *testing.T) {
	t.Setenv("AGENTGO_MEMORY_TOKENS", "600")
	t.Setenv("AGENTGO_MEMORY_AUTO_TOKENS", "100")
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	ctx := BindRunContext(context.Background(), "s")
	text := strings.Repeat("needle 中文 evidence ", 100)
	if err = s.Ingest(ctx, memory.Record{ID: "evidence", Scope: "s", Status: "active", Modality: "fact", Content: text}); err != nil {
		t.Fatal(err)
	}
	m := NewMemoryMiddleware(memory.NewPipeline(s))
	ctx, agent, err := m.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatal(err)
	}
	reader := agent.Tools[0].(tool.InvokableTool)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("needle")}}
	archived := []*schema.Message{}
	for turn := 0; turn < 40; turn++ {
		raw, e := reader.InvokableRun(ctx, `{"action":"read","record_id":"evidence","max_chars":300}`)
		if e != nil {
			t.Fatal(e)
		}
		var result memoryBrowseResult
		if e = json.Unmarshal([]byte(raw), &result); e != nil || len(result.Items) == 0 {
			t.Fatalf("reads stopped at step %d: %s %v", turn, raw, e)
		}
		msg := schema.ToolMessage(raw, fmt.Sprintf("call-%d", turn))
		msg.Name = "browse_memory"
		archived = append(archived, msg)
		state.Messages = append(state.Messages, msg)
		ctx, state, e = m.BeforeModelRewriteState(ctx, state, nil)
		if e != nil {
			t.Fatal(e)
		}
		spent := 0
		positions := memoryReadPositions(state.Messages)
		for i, msg := range state.Messages {
			if positions[i] || msg != nil && msg.Extra["agentgo.memory_context"] == true {
				spent += promptbudget.Estimate(msg.Content)
			}
		}
		if spent > 600 {
			t.Fatalf("resident budget exceeded at %d: %d", turn, spent)
		}
	}
	if !strings.Contains(archived[0].Content, "needle") {
		t.Fatal("archive mutated")
	}
	if memoryBudgetFrom(ctx).toolTokens != 0 {
		t.Fatal("pending batch not reclaimed")
	}
}
func TestMemoryResidentFoldingPreservesProtocolAndArchive(t *testing.T) {
	first := schema.ToolMessage(encodeMemoryResult(memoryBrowseResult{Generation: "run", Items: []memoryBrowseItem{{ID: "source", Text: strings.Repeat("old evidence ", 200)}}}), "first")
	first.Name = "browse_memory"
	second := schema.ToolMessage(encodeMemoryResult(memoryBrowseResult{Generation: "run", Items: []memoryBrowseItem{{ID: "latest", Text: "LATEST EVIDENCE"}}}), "second")
	second.Name = "browse_memory"
	other := schema.ToolMessage("not memory", "other")
	out, changed := foldMemoryReadMessages([]*schema.Message{first, second, other}, 160)
	if !changed || out[0].ToolCallID != first.ToolCallID || out[1].ToolCallID != second.ToolCallID || !strings.Contains(out[1].Content, "LATEST") || out[2] != other || !strings.Contains(first.Content, "old evidence") {
		t.Fatal(out)
	}
	if promptbudget.Estimate(out[0].Content)+promptbudget.Estimate(out[1].Content) > 160 {
		t.Fatal("overspent")
	}
	zero, _ := foldMemoryReadMessages(out, 0)
	if zero[0].Content != "" || zero[1].Content != "" {
		t.Fatal("zero budget retained data")
	}
}
func TestMemoryTreeToolExpandsSummaryToOriginal(t *testing.T) {
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := memory.NewPipeline(s)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err = s.Ingest(ctx, memory.Record{ID: fmt.Sprintf("record-%d", i), Scope: "s", Status: "active", Modality: "fact", Content: "source evidence", Metadata: map[string]any{"topic": fmt.Sprintf("topic-%d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.SummaryForest().Refresh(ctx, "s", nil); err != nil {
		t.Fatal(err)
	}
	invoke := func(in memoryBrowseInput) memoryBrowseResult {
		t.Helper()
		reader, e := newMemoryBrowseTool(s, p, "s", &memoryReadBudget{id: "tree", total: 4000})
		if e != nil {
			t.Fatal(e)
		}
		args, _ := json.Marshal(in)
		raw, e := reader.InvokableRun(ctx, string(args))
		if e != nil {
			t.Fatal(e)
		}
		var result memoryBrowseResult
		if e = json.Unmarshal([]byte(raw), &result); e != nil || len(result.Items) == 0 {
			t.Fatal(raw, e)
		}
		return result
	}
	root := invoke(memoryBrowseInput{Action: "tree"})
	if len(root.Items) != 1 || root.Items[0].Count != 10 {
		t.Fatal(root)
	}
	node := root.Items[0]
	for i := 0; i < 10 && node.RecordID == ""; i++ {
		page := invoke(memoryBrowseInput{Action: "tree", Node: node.ID})
		node = page.Items[0]
	}
	if node.RecordID == "" {
		t.Fatal("no original reachable")
	}
	read := invoke(memoryBrowseInput{Action: "read", RecordID: node.RecordID})
	if read.Items[0].Text != "source evidence" {
		t.Fatal(read)
	}
}
