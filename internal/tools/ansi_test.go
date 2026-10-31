package tools

import "testing"

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"plain":                      "plain",
		"\x1b[31mred\x1b[0m text":    "red text",
		"\x1b[1;32mok\x1b[m":         "ok",
		"a\x1b[2K\x1b[1Gb":           "ab",
		"\x1b]0;title\x07after":      "after",
		"\x1b]8;;http://x\x1b\\link": "link",
		"keep 100% [not escape]":     "keep 100% [not escape]",
		"cut\x1b[3":                  "cut",
		"\x1b(Bnormal":               "normal",
		"中文\x1b[33m黄\x1b[0m":         "中文黄",
	}
	for in, want := range cases {
		if got := stripANSIString(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanBashOutputKeepsExitCodeAndError(t *testing.T) {
	o, err := cleanBashOutput(bashOutput{Stdout: "\x1b[31mx\x1b[0m", Stderr: "\x1b[1me", ExitCode: 3}, nil)
	if err != nil || o.Stdout != "x" || o.Stderr != "e" || o.ExitCode != 3 {
		t.Fatalf("got %+v %v", o, err)
	}
}
