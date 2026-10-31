package sessions

import (
	"agentgo/internal/promptbudget"
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestPlanTaskProjectionShowsBlockersWithoutInventingVerification(t *testing.T) {
	tasks := []PlanTaskSnapshot{{ID: "1", Subject: "compile", Status: "pending", BlockedBy: []string{"2"}}, {ID: "2", Subject: "tests", Status: "in_progress"}, {ID: "3", Subject: "reported task", Status: "completed"}}
	view := CompileProjectedView([]*schema.Message{schema.UserMessage("continue")}, CompileOptions{PlanTasks: tasks})
	if len(view.TaskState) != 1 {
		t.Fatal("plan-only state was not rendered")
	}
	text := view.TaskState[0]
	for _, want := range []string{"Plan #2", "blocked_by=2", "verification requires tool evidence"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing "+want, text)
		}
	}
	if promptbudget.Estimate(text) > 1400 {
		t.Fatal("plan escaped budget")
	}
	rendered, _ := view.Render(context.Background())
	data, _ := json.Marshal(rendered)
	var restored []*schema.Message
	json.Unmarshal(data, &restored)
	// Fresh PlanTask storage is authoritative, not the old snapshot in Session metadata.
	after := CompileProjectedView(restored, CompileOptions{})
	if strings.Contains(strings.Join(after.TaskState, "\n"), "Plan #") {
		t.Fatal("deleted plans resurrected from session snapshot")
	}
}

func TestSummaryRevisionInvalidationPreservesSourcesAndArchive(t *testing.T) {
	original := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("GOAL keep tests"), schema.AssistantMessage("old response", nil), schema.UserMessage("current correction")}
	v := CompileProjectedView(original, CompileOptions{MaxActiveTurns: 1, PreserveLatestUser: true})
	v.memory.Summaries = []string{"STALE-SUMMARY"}
	v.memory.RecalledMemory = []string{"STALE-RECALL"}
	v.memory.MemoryRevision = "old"
	v.EpisodicSummary = []string{"STALE-SUMMARY"}
	v.RecalledMemory = []string{"STALE-RECALL"}
	rendered, _ := v.Render(context.Background())
	summary := ContextMessage("summary", "STALE-STANDALONE")
	StampSummaryRevision([]*schema.Message{summary}, "old")
	rendered = append(rendered, summary)
	cleaned, changed := InvalidateMemorySummaries(rendered, "new")
	if !changed {
		t.Fatal("stale memory was accepted")
	}
	text := renderedText(cleaned)
	if strings.Contains(text, "STALE-") {
		t.Fatal("stale visible context", text)
	}
	rebuilt := CompileProjectedView(cleaned, CompileOptions{})
	output, _ := rebuilt.Render(context.Background())
	text = renderedText(output)
	if strings.Contains(text, "STALE-") || !strings.Contains(text, "GOAL keep tests") || !strings.Contains(text, "current correction") {
		t.Fatal("stale summary revived or source notes lost", text)
	}
	if !strings.Contains(renderedText(rendered), "STALE-SUMMARY") {
		t.Fatal("archive was mutated")
	}
	if _, again := InvalidateMemorySummaries(cleaned, "new"); again {
		t.Fatal("invalidation is not idempotent")
	}
}

func TestLateSummaryFailsSourceVersionCheck(t *testing.T) {
	revision := "a"
	ctx := WithMemoryRevision(context.Background(), revision, func(context.Context) (string, error) { return revision, nil })
	revision = "b"
	if !errors.Is(ValidateMemoryRevision(ctx), ErrSummarySourceChanged) {
		t.Fatal("late summary source change ignored")
	}
}

func TestCorruptedProjectionDoesNotBrickConversationOrMutateArchive(t *testing.T) {
	runtime := ContextMessage("runtime", "OLD UNRELIABLE TASK CLAIM")
	runtime.Extra[extraProjectionMemory] = "{broken JSON"
	user := schema.UserMessage("recover my previous work")
	cleaned, changed := InvalidateMemorySummaries([]*schema.Message{schema.SystemMessage("KEEP RULES"), runtime, user}, "current")
	if !changed || len(cleaned) != 3 {
		t.Fatal("projection corruption blocked recovery")
	}
	if ContextKind(cleaned[1]) != "hygiene" || strings.Contains(renderedText(cleaned), "OLD UNRELIABLE") || cleaned[2] != user {
		t.Fatal("unsafe or damaged recovery", renderedText(cleaned))
	}
	out, _ := CompileProjectedView(cleaned, CompileOptions{}).Render(context.Background())
	if !strings.Contains(renderedText(out), "Retrieve original session history") || !strings.Contains(renderedText(out), "KEEP RULES") {
		t.Fatal("missing recovery guidance")
	}
	if runtime.Extra[extraProjectionMemory] != "{broken JSON" || runtime.Content != "OLD UNRELIABLE TASK CLAIM" {
		t.Fatal("archived input was modified")
	}
}
