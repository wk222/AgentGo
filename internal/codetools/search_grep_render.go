package codetools

// How grep lays out what it found: matching lines (optionally with context),
// the list of matching files, or the number of matches per file.
//
// Every answer that is a part of something bigger says so in its first line and
// names the offset that continues it. A bare "100 matches" reads as the total, and
// a model asked to count answered with the page size.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pageBounds clips the page [offset, offset+limit) to a list of n items.
func pageBounds(n, offset, limit int) (from, to int) {
	return min(offset, n), min(offset+limit, n)
}

// totalText is a total that is only a lower bound when the search was capped.
func totalText(n int, capped bool) string {
	if capped {
		return fmt.Sprintf("at least %d", n)
	}
	return fmt.Sprint(n)
}

const cappedReason = "the search stopped before it had seen everything (candidate limit or time limit), so the total is unknown"

// pastTheEnd answers an offset beyond the last result.
func pastTheEnd(offset, found int, capped bool, what string) string {
	if capped {
		return fmt.Sprintf("Nothing at offset %d: the search found only %d %s before it stopped. Narrow it with a more specific path, include glob or pattern.\n", offset, found, what)
	}
	return fmt.Sprintf("Nothing at offset %d: the search found only %d %s.\n", offset, found, what)
}

// pageFooter says how to get the rest of a cut answer; countHint adds the
// pointer to output_mode "count" for modes that do not already give counts.
func pageFooter(b *strings.Builder, next int, hasNext, capped, countHint bool) {
	var parts []string
	if hasNext {
		parts = append(parts, fmt.Sprintf("Next page: offset=%d.", next))
	}
	if capped {
		parts = append(parts, "Narrow the search with a more specific path, include glob or pattern to see the rest or to get exact numbers.")
	} else if hasNext && countHint {
		parts = append(parts, `output_mode "count" gives the number of matches per file.`)
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " ") + "\n")
	}
}

// --- content ---------------------------------------------------------------

func renderContent(res searchResult, offset, limit, ctxLines int) string {
	all := res.matches
	from, to := pageBounds(len(all), offset, limit)
	if from >= len(all) {
		return pastTheEnd(offset, len(all), res.capped, "matches")
	}
	page := all[from:to]
	more := to < len(all) || res.capped

	var b strings.Builder
	switch {
	case !more && offset == 0:
		fmt.Fprintf(&b, "%s in %s, newest file first.\n", plural(len(page), "match", "matches"), plural(countFiles(page), "file", "files"))
	case !more:
		fmt.Fprintf(&b, "Matches %d-%d of %d, newest file first.\n", from+1, to, len(all))
	case res.capped:
		fmt.Fprintf(&b, "Matches %d-%d of %s, newest file first, truncated: %s.\n", from+1, to, totalText(len(all), true), cappedReason)
	default:
		fmt.Fprintf(&b, "Matches %d-%d of %d, newest file first, truncated: this is one page of the results.\n", from+1, to, len(all))
	}
	if ctxLines > 0 {
		b.WriteString("(A line with a line:column prefix is a match; a line marked \"-\" is context.)\n")
	}
	writeMatches(&b, page, ctxLines)
	if more {
		pageFooter(&b, to, to < len(all), res.capped, true)
	}
	return b.String()
}

func countFiles(ms []grepMatch) int {
	n := 0
	for i, m := range ms {
		if i == 0 || m.path != ms[i-1].path {
			n++
		}
	}
	return n
}

// writeMatches lists matches under one heading per file. The matches arrive
// ordered, so the ones of a file are next to each other.
func writeMatches(b *strings.Builder, page []grepMatch, ctxLines int) {
	for i := 0; i < len(page); {
		j := i
		for j < len(page) && page[j].path == page[i].path {
			j++
		}
		group := page[i:j]
		fmt.Fprintf(b, "\n%s\n", filepath.ToSlash(group[0].path))
		if lines, ok := contextSource(group, ctxLines); ok {
			writeWithContext(b, group, lines, ctxLines)
		} else {
			writePlain(b, group)
		}
		i = j
	}
}

func writePlain(b *strings.Builder, group []grepMatch) {
	for _, m := range group {
		fmt.Fprintf(b, "  %d:%d: %s\n", m.lineNum, m.charNum, clip(m.lineText, maxMatchWidth))
	}
}

// contextSource reads the file when context is wanted and still fits what the
// search saw: a file that changed since, or one that is not text, gets plain
// match lines instead.
func contextSource(group []grepMatch, ctxLines int) ([]string, bool) {
	if ctxLines == 0 {
		return nil, false
	}
	lines, ok := readTextLines(group[0].path)
	if !ok || group[len(group)-1].lineNum > len(lines) {
		return nil, false
	}
	return lines, true
}

// writeWithContext prints each match with ctxLines lines on either side; spans
// that touch or overlap are shown as one block and separate blocks are divided
// by a "--" line.
func writeWithContext(b *strings.Builder, group []grepMatch, lines []string, ctxLines int) {
	type span struct{ from, to int }
	var spans []span
	hit := make(map[int]grepMatch, len(group))
	for _, m := range group {
		hit[m.lineNum] = m
		from, to := max(1, m.lineNum-ctxLines), min(len(lines), m.lineNum+ctxLines)
		if k := len(spans) - 1; k >= 0 && from <= spans[k].to+1 {
			spans[k].to = max(spans[k].to, to)
			continue
		}
		spans = append(spans, span{from, to})
	}
	for k, sp := range spans {
		if k > 0 {
			b.WriteString("  --\n")
		}
		for n := sp.from; n <= sp.to; n++ {
			text := clip(strings.TrimSpace(lines[n-1]), maxMatchWidth)
			if m, ok := hit[n]; ok {
				fmt.Fprintf(b, "  %d:%d: %s\n", n, m.charNum, text)
			} else {
				fmt.Fprintf(b, "  %d- %s\n", n, text)
			}
		}
	}
}

// readTextLines returns the lines of a text file; ok is false for a file that is
// binary, too big or unreadable.
func readTextLines(path string) (lines []string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() > maxWalkFileSize {
		return nil, false
	}
	head := make([]byte, binarySniffSize)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return nil, false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false
	}
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			lines = append(lines, strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return lines, err == io.EOF
		}
	}
}

// --- files and counts --------------------------------------------------------

// fileEntry is one matching file with the number of matches in it.
type fileEntry struct {
	path string
	mod  time.Time
	n    int
}

// fileEntries folds the matches into one entry per file, in the order the files
// first appear (newest first).
func fileEntries(ms []grepMatch) []fileEntry {
	var out []fileEntry
	for i, m := range ms {
		if i == 0 || m.path != ms[i-1].path {
			out = append(out, fileEntry{path: m.path, mod: m.modTime})
		}
		out[len(out)-1].n++
	}
	return out
}

func renderFiles(res searchResult, offset, limit int) string {
	entries := fileEntries(res.matches)
	from, to := pageBounds(len(entries), offset, limit)
	if from >= len(entries) {
		return pastTheEnd(offset, len(entries), res.capped, "files")
	}
	more := to < len(entries) || res.capped

	var b strings.Builder
	switch {
	case !more && offset == 0:
		fmt.Fprintf(&b, "%s match, newest first.\n\n", plural(len(entries), "file", "files"))
	case !more:
		fmt.Fprintf(&b, "Files %d-%d of %d, newest first.\n\n", from+1, to, len(entries))
	case res.capped:
		fmt.Fprintf(&b, "Files %d-%d of %s, newest first, truncated: %s.\n\n", from+1, to, totalText(len(entries), true), cappedReason)
	default:
		fmt.Fprintf(&b, "Files %d-%d of %d, newest first, truncated: this is one page of the results.\n\n", from+1, to, len(entries))
	}
	for _, e := range entries[from:to] {
		b.WriteString(filepath.ToSlash(e.path))
		b.WriteByte('\n')
	}
	if more {
		pageFooter(&b, to, to < len(entries), res.capped, true)
	}
	return b.String()
}

func renderCount(res searchResult, offset, limit int) string {
	entries := fileEntries(res.matches)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].n > entries[j].n })
	from, to := pageBounds(len(entries), offset, limit)
	if from >= len(entries) {
		return pastTheEnd(offset, len(entries), res.capped, "files")
	}
	more := to < len(entries) || res.capped

	var b strings.Builder
	if res.capped {
		fmt.Fprintf(&b, "At least %d matches in at least %s, files with the most matches first, truncated: %s; these are lower bounds.\n\n",
			len(res.matches), plural(len(entries), "file", "files"), cappedReason)
	} else {
		fmt.Fprintf(&b, "%s in %s, files with the most matches first.",
			plural(len(res.matches), "match", "matches"), plural(len(entries), "file", "files"))
		if to < len(entries) || offset > 0 {
			fmt.Fprintf(&b, " Showing files %d-%d.", from+1, to)
		}
		b.WriteString("\n\n")
	}
	for _, e := range entries[from:to] {
		fmt.Fprintf(&b, "%s: %d\n", filepath.ToSlash(e.path), e.n)
	}
	if more {
		pageFooter(&b, to, to < len(entries), res.capped, false)
	}
	return b.String()
}
