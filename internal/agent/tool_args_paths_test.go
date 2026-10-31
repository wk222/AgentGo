package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeArgs(t *testing.T, s string) map[string]any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, s)
	}
	return m
}

func repaired(t *testing.T, in string) map[string]any {
	t.Helper()
	out, err := prepareToolArguments(in)
	if err != nil {
		t.Fatalf("%s rejected: %v", in, err)
	}
	return decodeArgs(t, out)
}

func TestUnescapedWindowsPathsAreRepairedInsteadOfRejected(t *testing.T) {
	cases := []struct{ in, key, want string }{
		{`{"path":"C:\Users\user\a.txt"}`, "path", `C:\Users\user\a.txt`},
		{`{"pattern":"\d+\.log$"}`, "pattern", `\d+\.log$`},
		{`{"path":"C:\users\x"}`, "path", `C:\users\x`}, // \u followed by non-hex
		{`{"command":"dir C:\Users"}`, "command", `dir C:\Users`},
		{`{"path":"D:\报告\第一季.docx"}`, "path", `D:\报告\第一季.docx`},
		// Correct and incorrect escapes in one value: the correct ones must stay.
		{`{"path":"C:\\Users\user"}`, "path", `C:\Users` + `\user`},
		{`{"content":"say \"hi\" at C:\x"}`, "content", `say "hi" at C:\x`},
	}
	for _, c := range cases {
		if got := repaired(t, c.in)[c.key]; got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.in, c.key, got, c.want)
		}
	}
}

func TestValidEscapesInPathFieldsDoNotSilentlyBecomeControlCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"path":"C:\temp\new.txt"}`, `C:\temp\new.txt`},
		{`{"file_path":"C:\data\report\final.xlsx"}`, `C:\data\report\final.xlsx`},
		{`{"cwd":"D:\build\files"}`, `D:\build\files`},
	}
	for _, c := range cases {
		m := repaired(t, c.in)
		for k, v := range m {
			if v != c.want {
				t.Errorf("%s: %s = %q, want %q", c.in, k, v, c.want)
			}
		}
	}
	nested := repaired(t, `{"edits":[{"path":"C:\temp\a.txt","old":"x"}],"paths":["C:\temp\b","D:\new\c"]}`)
	edit := nested["edits"].([]any)[0].(map[string]any)
	if edit["path"] != `C:\temp\a.txt` || edit["old"] != "x" {
		t.Errorf("nested path not repaired: %v", edit)
	}
	paths := nested["paths"].([]any)
	if paths[0] != `C:\temp\b` || paths[1] != `D:\new\c` {
		t.Errorf("path list not repaired: %v", paths)
	}
}

func TestRepairLeavesOrdinaryArgumentsAlone(t *testing.T) {
	same := []string{
		`{"path":"C:\\Users\\user\\a.txt"}`,                      // already correct
		`{"content":"line1\nline2\tTab","path":"/tmp/x"}`,        // real escapes in a text field
		`{"path":"relative/dir/a.txt","n":12345678901234567890}`, // no backslash at all
	}
	for _, in := range same {
		out, err := prepareToolArguments(in)
		if err != nil || out != in {
			t.Errorf("changed or rejected a correct call:\n in: %s\nout: %s (%v)", in, out, err)
		}
	}
	// A control character in a non-path field is the model's text, not a path.
	m := repaired(t, `{"content":"C:\temp"}`)
	if m["content"] != "C:\temp" {
		t.Errorf("text field was rewritten: %q", m["content"])
	}
}

func TestRepairKeepsNumbersAndMarkupIntact(t *testing.T) {
	out, err := prepareToolArguments(`{"n":12345678901234567890,"path":"C:\temp\a&b<c>"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "12345678901234567890") {
		t.Errorf("number was rounded: %s", out)
	}
	if strings.Contains(out, `\u0026`) || strings.Contains(out, `\u003c`) {
		t.Errorf("markup characters were escaped: %s", out)
	}
	if repaired(t, out)["path"] != `C:\temp\a&b<c>` {
		t.Errorf("path wrong: %s", out)
	}
}

func TestTruncatedArgumentsAreStillRefused(t *testing.T) {
	for _, in := range []string{`{"content":"half a file`, `{"path":"C:\Users\wg`, `{"content":"abc\`} {
		if out, err := prepareToolArguments(in); err == nil {
			t.Errorf("a truncated call was accepted: %s -> %s", in, out)
		}
	}
}

func TestUnicodeEscapesSurviveInvalidEscapeRepair(t *testing.T) {
	m := repaired(t, `{"a":"\u00e9","p":"C:\x"}`)
	if m["a"] != "é" || m["p"] != `C:\x` {
		t.Errorf("got %v", m)
	}
}
