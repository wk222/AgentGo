// Package textenc turns the bytes of text files into UTF-8 text.
//
// Files on a Chinese Windows machine are often not UTF-8, and text that is passed
// on as a Go string without decoding it first loses its Chinese characters for good.
package textenc

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// Decode turns the bytes of a plain-text file into UTF-8 text. The second
// result names the encoding when it was not plain UTF-8, and is empty for plain
// UTF-8 (including pure ASCII).
//
// Files on a Chinese Windows machine are very often not UTF-8: Excel's "CSV
// (comma delimited)" and Notepad's "ANSI" both write GBK. Passing such bytes on
// as a string turns every Chinese character into U+FFFD, and that cannot be
// undone later, so the file has to be decoded here.
//
// The order is: a byte order mark, then valid UTF-8, then (for input that is not
// valid UTF-8) a choice between damaged UTF-8 and GB18030. GB18030 is the
// superset of GBK and of the Chinese Windows code page, so it is the default for
// anything else. Input in another legacy encoding, say Windows-1252, is not
// recognised and will come out as the wrong characters. A text of only a few
// GBK characters can happen to be valid UTF-8 and cannot be told apart.
func Decode(data []byte) (text, encoding string) {
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		return string(data[len(bomUTF8):]), "utf-8-bom"
	case bytes.HasPrefix(data, bomUTF16LE):
		return decodeUTF16(data[len(bomUTF16LE):], binary.LittleEndian), "utf-16le"
	case bytes.HasPrefix(data, bomUTF16BE):
		return decodeUTF16(data[len(bomUTF16BE):], binary.BigEndian), "utf-16be"
	case utf8.Valid(data):
		return string(data), ""
	case looksBinary(data):
		// Not text; decoding it as GB18030 would only invent different noise.
		return strings.ToValidUTF8(string(data), "\uFFFD"), "unknown"
	case mostlyUTF8(data):
		return strings.ToValidUTF8(string(data), "\uFFFD"), "utf-8-lossy"
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil {
		return strings.ToValidUTF8(string(data), "\uFFFD"), "unknown"
	}
	return string(out), "gb18030"
}

func decodeUTF16(b []byte, order binary.ByteOrder) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, order.Uint16(b[i:]))
	}
	s := string(utf16.Decode(units))
	if len(b)%2 == 1 {
		s += "\uFFFD" // a dangling byte is a truncated code unit
	}
	return s
}

// looksBinary reports a NUL byte near the start, which text in these encodings
// does not have (UTF-16 is recognised by its BOM before this is reached).
func looksBinary(data []byte) bool {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// mostlyUTF8 tells UTF-8 with a few damaged bytes from text in another
// encoding. GBK breaks UTF-8 nearly everywhere: of the two bytes of a character,
// about three in four do not form a UTF-8 sequence and leave two invalid bytes,
// so valid multi-byte sequences are far outnumbered. Real UTF-8 with a stray
// byte has the opposite balance.
//
// A ratio is used rather than "any invalid byte means GBK" because one damaged
// byte, say a truncated final character, must not turn a whole UTF-8 document
// into noise; and rather than a share of all bytes because a mostly-ASCII CSV
// with four Chinese header characters has very few invalid bytes in absolute
// terms and would be mistaken for UTF-8, losing exactly the headers.
func mostlyUTF8(data []byte) bool {
	var multi, bad int
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			bad++
		case size > 1:
			multi++
		}
		i += size
	}
	return multi >= 2*bad
}

// CharCount counts characters rather than bytes: a Chinese character is three
// bytes in UTF-8, and the figure is shown to a model that reads it as a length.
func CharCount(s string) int { return utf8.RuneCountInString(s) }
