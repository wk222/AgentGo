package coderuntime

import (
	"strings"
	"testing"
)

func TestApplyEditLadder(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		old, new string
		all      bool
		want     string
		rung     int
	}{
		{"literal", "a\nb\nc\n", "b", "B", false, "a\nB\nc\n", 0},
		{"delete whole line", "a\nb\nc\n", "b\n", "", false, "a\nc\n", 0},
		{"trailing whitespace", "x := 1  \ny := 2\n", "x := 1\ny := 2", "x := 9\ny := 2", false, "x := 9\ny := 2\n", 1},
		{"indent same style shift", "\tif a {\n\t\tf()\n\t}\n", "if a {\n\tf()\n}", "if a {\n\tg()\n}", false, "\tif a {\n\t\tg()\n\t}\n", 2},
		{"spaces to tabs", "func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n", "if x {\n    return 1\n}", "if x {\n    return 2\n}", false, "func f() {\n\tif x {\n\t\treturn 2\n\t}\n}\n", 2},
		{"inner runs", "a  =   b\n", "a = b", "a = c", false, "a = c\n", 3},
		{"curly quotes", "say(\"hi\")\n", "say(\u201chi\u201d)", "say(\"yo\")", false, "say(\"yo\")\n", 4},
		{"dash", "a - b\n", "a \u2013 b", "a + b", false, "a + b\n", 4},
		{"crlf in old", "a\nb\nc\n", "a\r\nb", "A\r\nB", false, "A\nB\nc\n", 1},
		{"replace all tolerant", "\tfoo bar\nbaz\n\tfoo bar\n", "  foo bar", "z", true, "z\nbaz\nz\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := applyEdit(c.content, c.old, c.new, c.all)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Content != c.want || got.Rung != c.rung {
				t.Fatalf("content %q rung %d; want %q rung %d", got.Content, got.Rung, c.want, c.rung)
			}
		})
	}
}

func TestApplyEditRefusals(t *testing.T) {
	t.Run("literal ambiguity", func(t *testing.T) {
		_, err := applyEdit("dup\ndup\n", "dup", "x", false)
		if err != errAmbiguousLiteral {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tolerant ambiguity does not fall through", func(t *testing.T) {
		_, err := applyEdit("\tx := 1\n\ty := 2\n\tx := 1\n", "  x := 1", "z", false)
		if err == nil || !strings.Contains(err.Error(), "matches 2 places") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("stricter rung wins over looser duplicates", func(t *testing.T) {
		// Block at lines 1-2 matches at the trailing rung; the block at lines
		// 4-5 would only match at the edge rung. The first rung to hit decides,
		// so the second block is neither a rival nor touched.
		got, err := applyEdit("k  \ny\nm\n  k  \ny\n", "k\ny", "z\ny", false)
		if err != nil || got.Content != "z\ny\nm\n  k  \ny\n" || got.Rung != 1 {
			t.Fatalf("got %+v err %v", got, err)
		}
	})
	t.Run("empty old", func(t *testing.T) {
		if _, err := applyEdit("a", "", "b", false); err != errEmptyOld {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("blank-only pattern", func(t *testing.T) {
		if _, err := applyEdit("a\n\nb\n", "  \n  ", "x", false); err == nil {
			t.Fatal("blank pattern must not match")
		}
	})
}

func TestNotFoundHints(t *testing.T) {
	content := "func f() {\n\ta := 1\n\tb := 2\n\tc := 3\n}\n"
	t.Run("closest region flags differing lines", func(t *testing.T) {
		_, err := applyEdit(content, "a := 1\nb := 9\nc := 3", "x", false)
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		for _, want := range []string{"Closest region: lines 2-4", "2 of 3", "Differing lines: 3", "→b := 2"} {
			if !strings.Contains(msg, want) {
				t.Errorf("missing %q in:\n%s", want, msg)
			}
		}
	})
	t.Run("already applied", func(t *testing.T) {
		_, err := applyEdit(content, "totally absent text", "b := 2\n\tc := 3", false)
		if err == nil || !strings.Contains(err.Error(), "may have been applied earlier") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nothing close", func(t *testing.T) {
		_, err := applyEdit(content, "zzz", "yyy", false)
		if err == nil || strings.Contains(err.Error(), "Closest region") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestToleranceNote(t *testing.T) {
	if toleranceNote(0) != "" {
		t.Fatal("literal match needs no note")
	}
	for r := 1; r <= len(ladder); r++ {
		if !strings.Contains(toleranceNote(r), ladder[r-1].label) {
			t.Errorf("rung %d note lacks its label", r)
		}
	}
}
