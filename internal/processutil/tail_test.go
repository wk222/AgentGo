package processutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTailBufferRetainsFinalUnicodeOutputAcrossSplitWrites(t *testing.T) {
	var b TailBuffer
	payload := strings.Repeat("\u6d4b\U0001f680", 10000) + "FINAL"
	for start := 0; start < len(payload); start += 13 {
		chunk := payload[start:min(start+13, len(payload))]
		if n, err := b.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatal(n, err)
		}
		if len(b.data) > tailBytes {
			t.Fatal("unbounded output")
		}
	}
	got := b.String()
	expected := []rune(payload)
	expected = expected[len(expected)-tailRunes:]
	if !utf8.ValidString(got) || got != string(expected) {
		t.Fatal("tail lost or corrupted")
	}
	b.Write([]byte(strings.Repeat("x", tailBytes*3) + "FINAL"))
	if got = b.String(); len(got) != tailRunes || !strings.HasSuffix(got, "FINAL") {
		t.Fatal("large write lost final output")
	}
}
