package visionocr

import (
	"strings"
	"testing"
)

// The validator stands between a vision model's output and the chunk store. A
// model that loops produces a long, plausible, useless block, so these tests pin
// "reject the whole thing" rather than "trim the tail".

func TestValidateRejectsDecodingLoop(t *testing.T) {
	// A real loop: one phrase repeated far beyond any plausible page.
	loop := strings.Repeat("本页无正文内容 ", 200)
	if _, err := Validate(loop); err == nil {
		t.Fatal("expected the repeated output to be rejected")
	} else if !strings.Contains(err.Error(), "repetitive_content") {
		t.Fatalf("wrong reason: %v", err)
	}
}

func TestValidateKeepsReasonableRepetition(t *testing.T) {
	// Tables legitimately repeat separators and short labels.
	var sb strings.Builder
	sb.WriteString("| 项目 | 金额 | 备注 |\n| --- | --- | --- |\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("| 差旅费 | 1200 | 已报销 |\n")
	}
	if _, err := Validate(sb.String()); err != nil {
		t.Fatalf("a table with repeated rows must pass: %v", err)
	}
}

func TestValidateTreatsNoTextAsAnAnswer(t *testing.T) {
	for _, raw := range []string{
		"no text", "No text.", "无文字内容", "图片中没有文字",
		"NO TEXT CONTENT!", "无法识别。",
	} {
		text, err := Validate(raw)
		if err != nil {
			t.Errorf("Validate(%q) returned an error; a no-text answer is not a failure: %v", raw, err)
		}
		if text != "" {
			t.Errorf("Validate(%q) = %q, want empty", raw, text)
		}
	}
}

func TestValidateRejectsEmptyAndUnreadable(t *testing.T) {
	for _, raw := range []string{"", "   \n\t", "| | |", "~~~"} {
		if _, err := Validate(raw); err == nil {
			t.Errorf("Validate(%q) should have failed", raw)
		}
	}
}

func TestValidateUnwrapsCodeFence(t *testing.T) {
	text, err := Validate("```markdown\n第一行\n第二行\n```")
	if err != nil {
		t.Fatal(err)
	}
	if text != "第一行\n第二行" {
		t.Fatalf("got %q", text)
	}
}

func TestValidateStripsHTMLWrapper(t *testing.T) {
	raw := `<html><body><div class="page"><p>发票号码：12345678</p><p>合计：99.00 元</p></div></body></html>`
	text, err := Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "<") {
		t.Fatalf("HTML survived: %q", text)
	}
	if !strings.Contains(text, "发票号码") || !strings.Contains(text, "99.00") {
		t.Fatalf("content lost: %q", text)
	}
}

func TestValidateConvertsHTMLTableToMarkdown(t *testing.T) {
	raw := `<table><tr><th>城市</th><th>销量</th></tr><tr><td>北京</td><td>120</td></tr></table>`
	text, err := Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "| 城市 | 销量 |") {
		t.Fatalf("header row not converted: %q", text)
	}
	if !strings.Contains(text, "| 北京 | 120 |") {
		t.Fatalf("body row not converted: %q", text)
	}
	if !strings.Contains(text, "| --- | --- |") {
		t.Fatalf("separator row missing, so a chunker would split the table: %q", text)
	}
}

func TestValidateEscapesPipesInTableCells(t *testing.T) {
	raw := `<table><tr><td>a|b</td><td>c</td></tr></table>`
	text, err := Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `a\|b`) {
		t.Fatalf("a pipe inside a cell would break the table: %q", text)
	}
}

func TestValidateCollapsesExcessBlankLines(t *testing.T) {
	text, err := Validate("第一段\n\n\n\n\n第二段")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "\n\n\n") {
		t.Fatalf("blank runs not collapsed: %q", text)
	}
}

// IsNoText must distinguish "the image has no text" from "the model misbehaved":
// the tool reports the first as found=false and the second as an error.
func TestIsNoText(t *testing.T) {
	_, err := Validate("no text")
	if err != nil {
		t.Fatalf("no-text answer returned an error: %v", err)
	}
	_, loopErr := Validate(strings.Repeat("本页无正文内容 ", 200))
	if loopErr == nil || IsNoText(loopErr) {
		t.Fatal("a decoding loop must not be reported as no-text")
	}
	if IsNoText(nil) {
		t.Fatal("IsNoText(nil) must be false")
	}
}
