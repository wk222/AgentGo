package bridge

import "testing"

func TestParseInlineEditResult(t *testing.T) {
	got, err := parseInlineEditResult(`{"replacement":"if err != nil {\n\treturn err\n}","summary":"补充错误处理"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Replacement != "if err != nil {\n\treturn err\n}" || got.Summary != "补充错误处理" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestParseInlineEditResultAcceptsJSONFence(t *testing.T) {
	got, err := parseInlineEditResult("```json\n{\"replacement\":\"const x = 1\",\"summary\":\"简化\"}\n```")
	if err != nil || got.Replacement != "const x = 1" {
		t.Fatalf("result=%#v err=%v", got, err)
	}
}

func TestParseInlineEditResultRejectsNarrative(t *testing.T) {
	if _, err := parseInlineEditResult("Here is the improved code"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseInlineEditResultAllowsDeletion(t *testing.T) {
	got, err := parseInlineEditResult(`{"replacement":"","summary":"删除冗余代码"}`)
	if err != nil || got.Replacement != "" {
		t.Fatalf("result=%#v err=%v", got, err)
	}
}
