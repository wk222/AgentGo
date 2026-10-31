package userhooks

import (
	"strings"
	"unicode/utf8"
)

// decodeOutput turns what a script printed into UTF-8 text.
//
// A script on a Chinese Windows usually prints GBK: cmd's echo, a Python
// script writing to a pipe, PowerShell 5 all use the system's ANSI code page
// unless told otherwise. Text that is not UTF-8 is therefore read in that code
// page; what still cannot be read becomes U+FFFD. A leading byte order mark is
// dropped.
func decodeOutput(s string) string {
	if t := dropPartialRune(s); looksLikeUTF8(t) {
		s = strings.ToValidUTF8(t, "\uFFFD")
	} else if text, ok := ansiToUTF8(s); ok {
		s = text
	} else {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	return strings.TrimPrefix(s, "\uFEFF")
}

// looksLikeUTF8 says whether s is UTF-8 text, as opposed to another code page.
// Valid text is; so is text with a few broken spots among plenty of valid
// characters, because the capture keeps the start and the end of a long output
// and cuts the rest, and a cut can fall inside a character. GBK looks nothing
// like that: nearly every byte of it is invalid as UTF-8.
func looksLikeUTF8(s string) bool {
	bad, good := 0, 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			bad++
		case n > 1:
			good++
		}
		i += n
	}
	return bad == 0 || good >= 2*bad
}

// dropPartialRune removes an unfinished UTF-8 character from the end of s.
func dropPartialRune(s string) string {
	for i := 1; i < utf8.UTFMax && i <= len(s); i++ {
		if utf8.RuneStart(s[len(s)-i]) {
			if !utf8.FullRuneInString(s[len(s)-i:]) {
				return s[:len(s)-i]
			}
			break
		}
	}
	return s
}
