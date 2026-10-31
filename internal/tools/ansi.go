package tools

import "bytes"

// stripANSI removes ANSI escape sequences (colors, cursor moves, OSC titles)
// from captured command output (adapted from octo-agent, MIT).
//
// Child processes color their output far more eagerly than "is stdout a
// terminal?" suggests (many tools only check that $TERM is set), so a piped
// command still emits SGR codes. A tool result renders the bytes it is given:
// ESC has no glyph and the rest of the sequence shows up as literal "[31m",
// which also costs the model tokens to read past.
func stripANSI(b []byte) []byte {
	if bytes.IndexByte(b, 0x1b) < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] != 0x1b {
			out = append(out, b[i])
			i++
			continue
		}
		i = skipEscape(b, i)
	}
	return out
}

func stripANSIString(s string) string {
	if bytes.IndexByte([]byte(s), 0x1b) < 0 {
		return s
	}
	return string(stripANSI([]byte(s)))
}

// skipEscape returns the index just past the escape sequence starting at i,
// where b[i] is ESC. An unterminated sequence swallows the rest of the input,
// which is also what a terminal does with it.
func skipEscape(b []byte, i int) int {
	j := i + 1
	if j >= len(b) {
		return len(b)
	}
	switch b[j] {
	case '[': // CSI: parameter bytes, intermediate bytes, one final byte
		j++
		for ; j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f; j++ {
		}
		for ; j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f; j++ {
		}
		if j < len(b) {
			j++
		}
		return j
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC: a string run ended by BEL or ST
		for j++; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2
			}
		}
		return len(b)
	default: // two- or three-byte escape: intermediate bytes, one final byte
		for ; j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f; j++ {
		}
		if j < len(b) {
			j++
		}
		return j
	}
}

// cleanBashOutput strips ANSI sequences from both streams of a shell result.
func cleanBashOutput(o bashOutput, err error) (bashOutput, error) {
	o.Stdout, o.Stderr = stripANSIString(o.Stdout), stripANSIString(o.Stderr)
	return o, err
}
