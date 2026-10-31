package sessions

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestRecallProjectionHonorsConfiguredBudgetWithoutRecuttingTree(t *testing.T) {
	payload := "Memory tree navigation\n" + strings.Repeat("[node=mt_reachable; n=8]主题摘要与原始来源索引\n", 65) + "FINAL_ROOT_LOCATOR"
	budget := 3000
	injected := schema.SystemMessage(tagMemoryInject + "\n" + payload)
	injected.Extra = map[string]any{"agentgo.memory_context": true}
	view := CompileProjectedView([]*schema.Message{injected, schema.UserMessage("continue")}, CompileOptions{RecallTokenBudget: &budget})
	if len(view.RecalledMemory) != 1 || view.RecalledMemory[0] != payload {
		t.Fatal("configured memory was cut again")
	}
	rendered, err := view.Render(context.Background())
	if err != nil || !strings.Contains(renderedText(rendered), "FINAL_ROOT_LOCATOR") {
		t.Fatal("root locator lost", err)
	}
}
func TestEmptyFreshRecallMarkerClearsInheritedMemory(t *testing.T) {
	old := CompileProjectedView([]*schema.Message{schema.UserMessage("first request")}, CompileOptions{})
	old.memory.RecalledMemory = []string{"STALE_RECALLED_FACT"}
	old.RecalledMemory = []string{"STALE_RECALLED_FACT"}
	archived, err := old.Render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marker := schema.SystemMessage("")
	marker.Extra = map[string]any{"agentgo.memory_context": true}
	budget := 0
	messages := append([]*schema.Message{marker}, archived...)
	view := CompileProjectedView(messages, CompileOptions{RecallTokenBudget: &budget})
	if !view.freshRecall || len(view.RecalledMemory) != 0 {
		t.Fatal("empty marker failed to clear recall", view.RecalledMemory)
	}
	rendered, err := view.Render(context.Background())
	if err != nil || strings.Contains(renderedText(rendered), "STALE_RECALLED_FACT") {
		t.Fatal("disabled memory revived", err)
	}
	if !strings.Contains(renderedText(archived), "STALE_RECALLED_FACT") {
		t.Fatal("archive changed")
	}
}
