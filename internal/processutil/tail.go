package processutil

import (
	"strings"
	"unicode/utf8"
)

// TailBuffer drains all output while retaining at most 1500 Unicode characters.
// It is not safe for concurrent writes. Using the same instance for Cmd.Stdout
// and Cmd.Stderr lets os/exec serialize their writes.
type TailBuffer struct{ data []byte }

const tailRunes = 1500
const tailBytes = tailRunes * utf8.UTFMax

func (b *TailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= tailBytes {
		b.data = append(b.data[:0], p[n-tailBytes:]...)
	} else {
		excess := len(b.data) + n - tailBytes
		if excess > 0 {
			b.data = append(b.data[:0], b.data[excess:]...)
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *TailBuffer) String() string {
	data := b.data
	// The byte window may start in the middle of a UTF-8 character.
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	text := []rune(strings.ToValidUTF8(string(data), "\uFFFD"))
	if len(text) > tailRunes {
		text = text[len(text)-tailRunes:]
	}
	return string(text)
}
