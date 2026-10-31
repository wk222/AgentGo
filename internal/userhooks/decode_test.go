package userhooks

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecodeOutputKeepsUTF8AsIs(t *testing.T) {
	for _, s := range []string{"", "plain ascii", "公司规定：禁止删除", "emoji 😀 ok"} {
		if got := decodeOutput(s); got != s {
			t.Errorf("decodeOutput(%q) = %q", s, got)
		}
	}
}

func TestDecodeOutputDropsABOM(t *testing.T) {
	if got := decodeOutput("\uFEFF{\"decision\":\"block\"}"); got != `{"decision":"block"}` {
		t.Errorf("a BOM must not reach the JSON reader: %q", got)
	}
}

// The capture stops at a byte count, which can fall inside a character. The
// text before it is still UTF-8 and must not be mistaken for another code page.
func TestDecodeOutputOnlyLosesTheCutCharacter(t *testing.T) {
	full := strings.Repeat("世", 10)
	for cut := 1; cut <= 2; cut++ {
		got := decodeOutput(full[:len(full)-cut])
		if got != strings.Repeat("世", 9) {
			t.Errorf("cut %d bytes off the end: %q", cut, got)
		}
	}
	emoji := strings.Repeat("😀", 5)
	for cut := 1; cut <= 3; cut++ {
		if got := decodeOutput(emoji[:len(emoji)-cut]); got != strings.Repeat("😀", 4) {
			t.Errorf("cut %d bytes off a four byte character: %q", cut, got)
		}
	}
}

// A long output is kept as its start, a marker and its end; both cuts can fall
// inside a character, and that must not turn the rest into another code page.
func TestDecodeOutputSurvivesCutsInTheMiddle(t *testing.T) {
	text := strings.Repeat("世", 200)
	capped := text[:3*100+2] + "\n[... 4000 bytes of output dropped ...]\n" + text[1:3*100]
	got := decodeOutput(capped)
	if !utf8.ValidString(got) || !strings.Contains(got, strings.Repeat("世", 99)) || strings.Contains(got, "涓") {
		t.Errorf("a long output with cuts inside characters came out as %q", got)
	}
}

func TestDecodeOutputNeverReturnsInvalidUTF8(t *testing.T) {
	for _, s := range []string{"\xff\xfe", "ok \x80 ok", "\xb9\xab\xcb\xbe", "\xe4\xb8"} {
		if got := decodeOutput(s); !utf8.ValidString(got) {
			t.Errorf("decodeOutput(%q) = %q is not valid UTF-8", s, got)
		}
	}
}
