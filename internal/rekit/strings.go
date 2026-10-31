package rekit

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"unicode/utf16"
)

// StrHit is one extracted string with its file offset.
type StrHit struct {
	Offset int64  `json:"offset"`
	Enc    string `json:"enc"`
	Text   string `json:"text"`
}

const maxStringWindow = 64 << 20

func printable(b byte) bool { return b >= 0x20 && b < 0x7f || b == '\t' }

// ExtractStrings scans [offset, offset+length) (capped at 64 MiB) for ASCII and UTF-16LE
// strings. filter is an optional regexp over the text. Returns hits, the offset where the
// scan stopped, and whether the window ended before EOF.
func ExtractStrings(path string, offset, length int64, minLen int, wide bool, filter string) (hits []StrHit, scanned int64, eof bool, err error) {
	var re *regexp.Regexp
	if filter != "" {
		if re, err = regexp.Compile(filter); err != nil {
			return nil, 0, false, fmt.Errorf("bad filter regexp: %w", err)
		}
	}
	if minLen < 3 {
		minLen = 4
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if offset < 0 || offset > st.Size() {
		return nil, 0, false, fmt.Errorf("offset %d outside file of %d bytes", offset, st.Size())
	}
	if length <= 0 || length > maxStringWindow {
		length = maxStringWindow
	}
	buf := make([]byte, length)
	n, rerr := f.ReadAt(buf, offset)
	if rerr != nil && rerr != io.EOF {
		return nil, 0, false, rerr
	}
	buf = buf[:n]
	eof = offset+int64(n) >= st.Size()
	keep := func(off int64, enc, s string) {
		if re == nil || re.MatchString(s) {
			hits = append(hits, StrHit{off, enc, s})
		}
	}
	// ASCII
	start := -1
	for i := 0; i <= len(buf); i++ {
		if i < len(buf) && printable(buf[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= minLen {
			keep(offset+int64(start), "ascii", string(buf[start:i]))
		}
		start = -1
	}
	if wide {
		var run []uint16
		var runStart int
		flush := func() {
			if len(run) >= minLen {
				keep(offset+int64(runStart), "utf16le", string(utf16.Decode(run)))
			}
			run = run[:0]
		}
		for i := 0; i+1 < len(buf); i += 2 {
			c := uint16(buf[i]) | uint16(buf[i+1])<<8
			if c >= 0x20 && c < 0x7f || c >= 0x4e00 && c <= 0x9fff {
				if len(run) == 0 {
					runStart = i
				}
				run = append(run, c)
			} else {
				flush()
			}
		}
		flush()
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Offset < hits[j].Offset })
	return hits, int64(n), eof, nil
}
