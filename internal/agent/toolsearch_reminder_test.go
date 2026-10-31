package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
)

func TestIsDeepSeekFamily(t *testing.T) {
	for _, m := range []string{"ds", "DS", "deepseek", "deepseek-v4-flash", "moma/dsv4", "ds-chat"} {
		if !isDeepSeekFamily(m) {
			t.Errorf("%q should be DeepSeek family", m)
		}
	}
	for _, m := range []string{"", "gpt-6-luna", "glm-5.2", "gemini-3.5-flash", "moma/glm52-w4a8", "claude-ds1"} {
		if isDeepSeekFamily(m) {
			t.Errorf("%q must keep the default reminder", m)
		}
	}
}

func TestToolSearchConfigReminderByModel(t *testing.T) {
	plain := toolSearchConfig(withModelName(context.Background(), "gpt-6-luna"), &toolsearch.Config{})
	if plain.CustomFormatReminder != nil {
		t.Fatal("non-DeepSeek models keep the default reminder")
	}
	ds := toolSearchConfig(withModelName(context.Background(), "ds"), &toolsearch.Config{})
	if ds.CustomFormatReminder == nil {
		t.Fatal("DeepSeek models get the keyword reminder")
	}
	out, err := ds.CustomFormatReminder(context.Background(), &toolsearch.FormatReminderInput{})
	if err != nil || out.Reminder != "" {
		t.Fatalf("DeepSeek gets no trailing reminder, got %q err %v", out.Reminder, err)
	}
	if h := toolSearchInstructionHint("ds"); h == "" || h[1:] != keywordReminder {
		t.Fatalf("DeepSeek hint should carry the keyword text, got %q", h)
	}
	if toolSearchInstructionHint("gpt-6-luna") != "" {
		t.Fatal("other models add nothing to the instruction")
	}
}
