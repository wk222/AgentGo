package processutil

import (
	"strings"
	"testing"
)

func TestOutputBufferDrainsAndBoundsLargeWrites(t *testing.T) {
	var b OutputBuffer
	p := []byte(strings.Repeat("x", MaxOutputBytes+100))
	for i := 0; i < 3; i++ {
		n, err := b.Write(p)
		if err != nil || n != len(p) {
			t.Fatal("writer stopped draining", n, err)
		}
	}
	if len(b.data) != MaxOutputBytes || !strings.Contains(b.String(), "output truncated") {
		t.Fatal("output unbounded or truncation silent")
	}
}
func TestOutputBufferPreservesShortOutput(t *testing.T) {
	var b OutputBuffer
	_, _ = b.Write([]byte("hello"))
	if b.String() != "hello" {
		t.Fatal(b.String())
	}
}
