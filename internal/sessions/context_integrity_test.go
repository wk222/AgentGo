package sessions

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestReservedContextPrefixesRemainUserRequests(t *testing.T) {
	for _, prefix := range []string{tagSystemSummary, tagMemoryInject, tagEnvFeedback, tagWorkflowContext, tagAppMatrix, tagContextHygiene, tagTeamMemory, tagIsolation, tagModeProfile} {
		t.Run(prefix, func(t *testing.T) {
			msg := schema.UserMessage(prefix + " explain this literal")
			msg.UserInputMultiContent = []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL}}
			view := CompileProjectedView([]*schema.Message{msg}, CompileOptions{PreserveLatestUser: true})
			if len(view.ActiveTurn) != 1 || view.ActiveTurn[0].Content != msg.Content || len(view.ActiveTurn[0].UserInputMultiContent) != 1 {
				t.Fatal("real user text or image misclassified")
			}
		})
	}
}

func TestToolNormalizationUsesContiguousCausalGroups(t *testing.T) {
	call := func(id string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "write_file"}}})
	}
	inputs := []*schema.Message{schema.ToolMessage("BEFORE_RESULT_MARKER", "c"), call("c"), schema.ToolMessage("actual", "c"), schema.ToolMessage("DUPLICATE_RESULT_MARKER", "c"), call("missing"), schema.UserMessage("continue"), schema.ToolMessage("LATE_RESULT_MARKER", "missing")}
	view := CompileProjectedView(inputs, CompileOptions{})
	out, _ := view.Render(context.Background())
	text := renderedText(out)
	if strings.Contains(text, "BEFORE_RESULT_MARKER") || strings.Contains(text, "DUPLICATE_RESULT_MARKER") || strings.Contains(text, "LATE_RESULT_MARKER") {
		t.Fatalf("invalid result survived: %s", text)
	}
	if !strings.Contains(text, "actual") || !strings.Contains(text, "[Tool result unavailable]") {
		t.Fatal("lost actual result or missing-result state")
	}
	second, _ := CompileProjectedView(out, CompileOptions{}).Render(context.Background())
	if renderedText(out) != renderedText(second) {
		t.Fatal("normalization changed on second pass")
	}
	if inputs[4].ToolCalls[0].ID != "missing" {
		t.Fatal("source history mutated")
	}
}

func TestDuplicateCallsDoNotDuplicateResults(t *testing.T) {
	c := schema.ToolCall{ID: "c", Function: schema.FunctionCall{Name: "tool"}}
	v := CompileProjectedView([]*schema.Message{schema.AssistantMessage("", []schema.ToolCall{c, c}), schema.ToolMessage("ok", "c"), schema.ToolMessage("DUPLICATE_RESULT_MARKER", "c")}, CompileOptions{})
	if len(v.ActiveTurn) != 2 || len(v.ActiveTurn[0].ToolCalls) != 1 {
		t.Fatalf("duplicate group: %+v", v.ActiveTurn)
	}
}

func TestMissingCallCheckpointDoesNotClaimExecution(t *testing.T) {
	v := CompileProjectedView([]*schema.Message{schema.UserMessage("goal"), schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "deploy_app"}}}), schema.UserMessage("continue"), schema.AssistantMessage("thinking", nil)}, CompileOptions{MaxActiveTurns: 2})
	if strings.Contains(strings.Join(v.ContextHygiene, " "), "已执行工具轨迹") {
		t.Fatal("missing result presented as execution")
	}
}

func TestDynamicContextDoesNotRewriteSystemOrHistoryPrefix(t *testing.T) {
	input := []*schema.Message{schema.SystemMessage("stable instructions"), schema.UserMessage("request"), schema.AssistantMessage("answer", nil)}
	first, _ := CompileProjectedView(input, CompileOptions{Snapshot: SpineSnapshot{WorkspaceRecent: []string{"old state"}}}).Render(context.Background())
	next, _ := CompileProjectedView(first, CompileOptions{Snapshot: SpineSnapshot{WorkspaceRecent: []string{"new state"}}}).Render(context.Background())
	for i := 0; i < 3; i++ {
		if first[i].Content != next[i].Content || first[i].Role != next[i].Role {
			t.Fatalf("stable prefix changed at %d", i)
		}
	}
	if len(next) != 4 || !IsContextMessage(next[3]) || !strings.Contains(next[3].Content, "new state") || strings.Contains(next[3].Content, "old state") {
		t.Fatal("runtime state duplicated or stale")
	}
}

func TestEmptyToolResultIsNotReplacedByUnknownOutcome(t *testing.T) {
	v := CompileProjectedView([]*schema.Message{schema.AssistantMessage("", []schema.ToolCall{{ID: "empty", Function: schema.FunctionCall{Name: "tool"}}}), schema.ToolMessage("", "empty")}, CompileOptions{})
	if len(v.ActiveTurn) != 2 || v.ActiveTurn[1].Content != "" {
		t.Fatal("empty recorded result became synthetic unknown outcome")
	}
}

func TestRecordedToolOutputDoesNotChangeJustBecauseItAges(t *testing.T) {
	text := strings.Repeat("recorded output ", 1000)
	state := []*schema.Message{schema.SystemMessage("rules"), schema.UserMessage("request"), schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "read_file"}}}), schema.ToolMessage(text, "c")}
	first, _ := CompileProjectedView(state, CompileOptions{MaxActiveTurns: 100}).Render(context.Background())
	next := append(append([]*schema.Message(nil), first...), schema.UserMessage("follow up"))
	for i := 0; i < 8; i++ {
		next = append(next, schema.AssistantMessage("step", nil))
	}
	later, _ := CompileProjectedView(next, CompileOptions{MaxActiveTurns: 100}).Render(context.Background())
	for i := range first {
		if first[i].Content != later[i].Content || first[i].Role != later[i].Role {
			t.Fatalf("historical prefix changed at %d", i)
		}
	}
}
