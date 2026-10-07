package bridge

import (
	"errors"
	"strings"
	"testing"
)

// A failed agent run that falls back to tool-less chat must say so in the text
// the user reads, and flag the result for programmatic callers.
func TestDegradedReplyIsExplicit(t *testing.T) {
	res := degradedReply(errors.New("gob: type not registered\nfor interface: map[string]interface {}"), "这里是普通回答")

	if res.Degraded == "" {
		t.Fatal("Degraded must be set")
	}
	if res.Error != "" {
		t.Fatalf("a degraded reply is not an error: %q", res.Error)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d", len(res.Messages))
	}
	got := res.Messages[0].Content
	for _, want := range []string{"能力降级", "gob: type not registered", "不带工具", "不代表任务已完成", "这里是普通回答"} {
		if !strings.Contains(got, want) {
			t.Errorf("reply lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(res.Degraded, "\n") {
		t.Errorf("reason should be one line: %q", res.Degraded)
	}
	if !strings.HasSuffix(got, "这里是普通回答") {
		t.Errorf("the model's answer must follow the notice:\n%s", got)
	}
}

func TestDegradedReplyBoundsLongReasons(t *testing.T) {
	res := degradedReply(errors.New(strings.Repeat("错", 5000)), "ok")
	if n := len([]rune(res.Degraded)); n > degradedReasonMax+1 {
		t.Fatalf("reason has %d runes", n)
	}
	if len(res.Messages[0].Content) > 2000 {
		t.Fatalf("notice is not bounded: %d bytes", len(res.Messages[0].Content))
	}
}
