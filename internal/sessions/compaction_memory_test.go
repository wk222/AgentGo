package sessions

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactionMemorySurvivesRepeatedProjection(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("原始目标：优化 agentgo，不修改 Eino 底层"), schema.AssistantMessage("记录目标", nil), schema.UserMessage("补充约束：必须保留所有已有测试"), schema.AssistantMessage("收到", nil), schema.UserMessage("现在继续处理上下文")}
	for i := 0; i < 80; i++ {
		id := fmt.Sprint(i)
		msgs = append(msgs, schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "read_file", Arguments: "{}"}}}), schema.ToolMessage("verified-result", id))
		v := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 6, PreserveLatestUser: true})
		var err error
		msgs, err = v.Render(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	all := ""
	for _, m := range msgs {
		all += m.Content
	}
	for _, want := range []string{"不修改 Eino 底层", "必须保留所有已有测试", "现在继续处理上下文"} {
		if !strings.Contains(all, want) {
			t.Fatalf("lost constraint %s", want)
		}
	}
	if len(all) > 30000 || strings.Count(all, "<system_rules>") != 1 {
		t.Fatalf("prompt growth: %d", len(all))
	}
}
func TestProjectionRetainsSummaryWithoutBasePrompt(t *testing.T) {
	v := CompileProjectedView([]*schema.Message{ContextMessage("summary", "[System Summary] earlier verified facts"), schema.UserMessage("next")}, CompileOptions{})
	first, _ := v.Render(context.Background())
	second, _ := CompileProjectedView(first, CompileOptions{}).Render(context.Background())
	if first[0].Content != second[0].Content || !strings.Contains(renderedText(second), "earlier verified facts") {
		t.Fatal("summary lost or recursively wrapped")
	}
}
func TestCompressionKeepsLatestImageRequestAndMetadata(t *testing.T) {
	u := schema.UserMessage("")
	u.Extra = map[string]any{"agentgo.attachments_checked": "session"}
	u.UserInputMultiContent = []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL}}
	msgs := []*schema.Message{u, schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "inspect"}}}), schema.ToolMessage("ok", "c")}
	out, _ := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 2, PreserveLatestUser: true}).Render(context.Background())
	found := false
	for _, m := range out {
		if m.Role == schema.User && len(m.UserInputMultiContent) > 0 {
			found = true
			if m.Extra["agentgo.attachments_checked"] != "session" {
				t.Fatal("lost metadata")
			}
		}
	}
	if !found {
		t.Fatal("lost image-only current request")
	}
}
func TestStaleToolSnippingPreservesUTF8(t *testing.T) {
	msgs := []FlatMessage{{Role: "tool", Content: strings.Repeat("中文🐈", 4000)}, {Role: "user", Content: "tail"}}
	out, n := snipStaleToolResults(msgs, 1, 1001)
	if n != 1 || !utf8.ValidString(out[0].Content) {
		t.Fatal("invalid unicode snip")
	}
}

func TestProjectionKeepsReasoningMetadataWithoutMutatingSource(t *testing.T) {
	a := schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "t"}}})
	a.ReasoningContent = "provider reasoning"
	tool := schema.ToolMessage(strings.Repeat("long result", 1000), "c")
	messages := []*schema.Message{schema.UserMessage("request"), a, tool}
	out, _ := CompileProjectedView(messages, CompileOptions{HardToolResultChars: 1000}).Render(context.Background())
	for _, m := range out {
		if m.Role == schema.Assistant && m.ReasoningContent != "provider reasoning" {
			t.Fatal("lost reasoning required for tool continuation")
		}
	}
	if len(tool.Content) < 10000 {
		t.Fatal("compression mutated original tool output")
	}
}

func renderedText(messages []*schema.Message) string {
	var parts []string
	for _, m := range messages {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n")
}
