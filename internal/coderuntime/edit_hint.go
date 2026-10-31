package coderuntime

// Error text and hints for failed or approximate edits. Scores computed here
// only inform the model; they never decide where an edit lands.

import (
	"fmt"
	"strings"
)

const (
	minHintLen = 12 // shortest new_string worth checking for "already applied"
	hintMargin = 1  // context lines shown around the closest region
)

func ambiguousTolerantError(n int, label string) error {
	return fmt.Errorf("old_string matches %d places once differences in %s are ignored. Add neighbouring lines so only one place matches, or set replace_all to true", n, label)
}

// notFoundError explains a failed match and, where possible, shows the
// closest region of the file.
func notFoundError(content, oldStr, newStr string) error {
	var b strings.Builder
	b.WriteString("old_string was not found in the file, even ignoring differences in whitespace and typographic punctuation. View the file again and copy the target lines exactly.")
	lines := strings.Split(content, "\n")
	pat, _ := patternLines(oldStr)
	if hint := closestRegion(lines, pat); hint != "" {
		b.WriteString("\n\n" + hint)
	} else if hint := firstLineHint(lines, pat); hint != "" {
		b.WriteString("\n\n" + hint)
	}
	if t := strings.TrimSpace(newStr); len(t) >= minHintLen && strings.Contains(content, t) {
		b.WriteString("\n\nnew_string already appears in the file, so this edit may have been applied earlier.")
	}
	return fmt.Errorf("%s", b.String())
}

// toleranceNote tells the model its old_string needed a looser comparison.
func toleranceNote(rung int) string {
	if rung <= 0 || rung > len(ladder) {
		return ""
	}
	return fmt.Sprintf("Note: old_string differed from the file only in %s. The edit was applied to the matching lines and new_string was re-indented to fit. Check the result.", ladder[rung-1].label)
}

func withToleranceNote(msg string, rung int) string {
	if note := toleranceNote(rung); note != "" {
		return msg + "\n" + note
	}
	return msg
}

// closestRegion finds the window of file lines that agrees with pat on the
// most lines (compared trimmed) and prints it, flagging the lines that differ.
// Reported only when at least half the lines agree.
func closestRegion(lines, pat []string) string {
	pat = trimBlankEnds(pat)
	if len(pat) == 0 || len(lines) < len(pat) {
		return ""
	}
	want := make([]string, len(pat))
	for i, p := range pat {
		want[i] = strings.TrimSpace(p)
	}
	bestStart, bestHits := -1, 0
	for s := 0; s+len(want) <= len(lines); s++ {
		hits := 0
		for j, w := range want {
			if strings.TrimSpace(lines[s+j]) == w {
				hits++
			}
		}
		if hits > bestHits {
			bestStart, bestHits = s, hits
		}
	}
	if bestStart < 0 || bestHits*2 < len(want) {
		return ""
	}
	var differ []string
	for j, w := range want {
		if strings.TrimSpace(lines[bestStart+j]) != w {
			differ = append(differ, fmt.Sprint(bestStart+j+1))
		}
	}
	end := bestStart + len(want) - 1
	var b strings.Builder
	fmt.Fprintf(&b, "Closest region: lines %d-%d, where %d of %d lines agree with old_string.\n", bestStart+1, end+1, bestHits, len(want))
	for i := max(0, bestStart-hintMargin); i <= min(len(lines)-1, end+hintMargin); i++ {
		fmt.Fprintf(&b, "%6d|%s\n", i+1, showWhitespace(lines[i]))
	}
	if len(differ) > 0 {
		fmt.Fprintf(&b, "Differing lines: %s.\n", strings.Join(differ, ", "))
	}
	b.WriteString("(→ is a tab, · a leading space)")
	return b.String()
}

// firstLineHint reports where old_string's first line occurs when the rest of
// the block does not follow it.
func firstLineHint(lines, pat []string) string {
	pat = trimBlankEnds(pat)
	if len(pat) == 0 {
		return ""
	}
	first := strings.TrimSpace(pat[0])
	for i, l := range lines {
		if strings.TrimSpace(l) == first {
			return fmt.Sprintf("The first line of old_string occurs at line %d, but the lines after it differ.", i+1)
		}
	}
	return ""
}

func trimBlankEnds(lines []string) []string {
	for len(lines) > 0 && isBlank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// showWhitespace makes tabs and the leading space run visible.
func showWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\t", "→")
	body := strings.TrimLeft(s, " ")
	return strings.Repeat("·", len(s)-len(body)) + body
}
