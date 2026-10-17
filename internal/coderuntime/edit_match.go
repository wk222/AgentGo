package coderuntime

// Whitespace-tolerant find-and-replace for the native edit tools.
//
// The matching strategy (exact match first, then a whitespace-normalised
// whole-line fallback with indentation adaptation, then a diagnostic hint) is
// derived from Crush's edit tool (third_party/crush, FSL-1.1-MIT), reduced to
// pure string functions with no session, permission or history coupling.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// whitespaceCorrectedNote tells the caller that old_string did not match
// byte-for-byte and that the edit was applied to whitespace-equivalent text.
const whitespaceCorrectedNote = "Note: old_string did not match exactly. The edit was applied to whitespace-equivalent text in the file and new_string was re-indented to match the file's style. Verify the result."

// errAmbiguous is returned when old_string matches more than once and
// replace_all is off.
var errAmbiguous = errors.New("old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true")

// FindAndReplace replaces old with new in content. Without replaceAll it needs
// exactly one match. The bool reports whether the whitespace-normalised
// fallback was needed.
func FindAndReplace(content, old, new string, replaceAll bool) (string, bool, error) {
	if replaceAll {
		if strings.Contains(content, old) {
			return strings.ReplaceAll(content, old, new), false, nil
		}
	} else {
		index := strings.Index(content, old)
		switch {
		case index == -1:
			// fall through to the fuzzy fallback
		case index != strings.LastIndex(content, old):
			return "", false, errAmbiguous
		default:
			return content[:index] + new + content[index+len(old):], false, nil
		}
	}
	if result, ok := normalizedReplace(content, old, new, replaceAll); ok {
		return result, true, nil
	}
	return "", false, notFoundError(content, old)
}

func notFoundError(content, old string) error {
	msg := "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks"
	if hint := diagnoseMismatch(content, old); hint != "" {
		msg += "\n\n" + hint
	}
	return errors.New(msg)
}

// withWhitespaceNote appends the auto-correction note when the edit did not
// match the file byte-for-byte.
func withWhitespaceNote(message string, corrected bool) string {
	if !corrected {
		return message
	}
	return message + "\n" + whitespaceCorrectedNote
}

// normMatch is a line range [startLine, endLine] of the original content.
type normMatch struct{ startLine, endLine int }

// findNormalizedMatches searches content for old after collapsing every line's
// whitespace runs. Only whole-line matches are reported, because replacement
// happens at line granularity.
func findNormalizedMatches(content, old string) []normMatch {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(old, "\n")

	normOld := joinNormalized(oldLines)
	if strings.TrimSpace(normOld) == "" {
		return nil
	}

	normContentLines := make([]string, len(contentLines))
	for i, l := range contentLines {
		normContentLines[i] = normalizeWS(l)
	}
	normContent := strings.Join(normContentLines, "\n")

	var matches []normMatch
	searchFrom := 0
	for searchFrom <= len(normContent) {
		idx := strings.Index(normContent[searchFrom:], normOld)
		if idx == -1 {
			break
		}
		absIdx := searchFrom + idx
		end := absIdx + len(normOld)
		atLineStart := absIdx == 0 || normContent[absIdx-1] == '\n'
		atLineEnd := end == len(normContent) || normContent[end] == '\n'
		if !atLineStart || !atLineEnd {
			searchFrom = absIdx + 1
			continue
		}
		startLine := lineAtOffset(normContentLines, absIdx)
		endLine := startLine + len(oldLines) - 1
		if endLine >= len(contentLines) {
			break
		}
		matches = append(matches, normMatch{startLine, endLine})
		searchFrom = end + 1
	}
	return matches
}

// normalizedReplace is the fuzzy fallback: a unique (or replaceAll) whole-line
// match is replaced, with new_string re-indented to the file's style.
func normalizedReplace(content, old, new string, replaceAll bool) (string, bool) {
	matches := findNormalizedMatches(content, old)
	if len(matches) == 0 {
		return "", false
	}
	if !replaceAll && len(matches) > 1 {
		return "", false // ambiguous; let the model disambiguate
	}

	contentLines := strings.Split(content, "\n")
	fileUnit := detectIndentUnit(contentLines)

	result := slices.Clone(contentLines)
	for _, m := range slices.Backward(matches) { // reverse keeps indices valid
		actual := strings.Join(contentLines[m.startLine:m.endLine+1], "\n")
		adapted := adaptIndentation(actual, old, new, fileUnit)
		result = slices.Concat(result[:m.startLine], strings.Split(adapted, "\n"), result[m.endLine+1:])
	}
	return strings.Join(result, "\n"), true
}

func joinNormalized(lines []string) string {
	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = normalizeWS(l)
	}
	return strings.Join(norm, "\n")
}

// adaptIndentation rewrites newStr's leading whitespace to the file's style
// and applies the nesting-depth offset between old_string and the matched text.
func adaptIndentation(actualStr, oldStr, newStr, fileUnit string) string {
	if fileUnit == "" {
		return newStr
	}
	actualLines := strings.Split(actualStr, "\n")
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	sourceUnit := detectIndentUnit(oldLines)
	if sourceUnit == "" {
		sourceUnit = detectIndentUnit(newLines)
	}
	if sourceUnit == "" {
		sourceUnit = fileUnit
	}

	baseDepth := firstIndentDepth(actualLines, fileUnit)
	oldBaseDepth := firstIndentDepth(oldLines, sourceUnit)
	depthOffset := baseDepth - oldBaseDepth
	if sourceUnit == fileUnit && depthOffset == 0 {
		return newStr
	}

	result := make([]string, len(newLines))
	for i, line := range newLines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			result[i] = line
			continue
		}
		leading := line[:len(line)-len(trimmed)]
		depth := max(measureDepth(leading, sourceUnit)+depthOffset, 0)
		result[i] = strings.Repeat(fileUnit, depth) + trimmed
	}
	return strings.Join(result, "\n")
}

func firstIndentDepth(lines []string, unit string) int {
	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " \t")
		if trimmed == "" {
			continue
		}
		return measureDepth(l[:len(l)-len(trimmed)], unit)
	}
	return 0
}

// detectIndentUnit returns "\t" for tab-indented text, N spaces for
// space-indented text, or "" when it cannot tell.
func detectIndentUnit(lines []string) string {
	minSpaces := 0
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		leading := line[:len(line)-len(trimmed)]
		if leading == "" {
			continue
		}
		if strings.Contains(leading, "\t") {
			return "\t"
		}
		if n := len(leading); minSpaces == 0 || n < minSpaces {
			minSpaces = n
		}
	}
	if minSpaces > 0 {
		return strings.Repeat(" ", minSpaces)
	}
	return ""
}

func measureDepth(leading, unit string) int {
	if unit == "" {
		return 0
	}
	if unit == "\t" {
		return strings.Count(leading, "\t")
	}
	return strings.Count(leading, " ") / len(unit)
}

// diagnoseMismatch builds a hint for a failed match: the whitespace-different
// text if it exists, else the closest line window.
func diagnoseMismatch(content, old string) string {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(old, "\n")
	if len(oldLines) == 0 {
		return ""
	}
	if hint := diagnoseWhitespaceMismatch(contentLines, oldLines); hint != "" {
		return hint
	}
	return diagnoseBestLineMatch(contentLines, oldLines)
}

func normalizeWS(s string) string { return strings.Join(strings.Fields(s), " ") }

func diagnoseWhitespaceMismatch(contentLines, oldLines []string) string {
	matches := findNormalizedMatches(strings.Join(contentLines, "\n"), strings.Join(oldLines, "\n"))
	if len(matches) == 0 {
		return ""
	}
	return formatWhitespaceHint(contentLines, matches[0].startLine, matches[0].endLine)
}

// lineAtOffset maps a character offset in newline-joined lines to a line index.
func lineAtOffset(lines []string, offset int) int {
	pos := 0
	for i, line := range lines {
		if pos+len(line) >= offset {
			return i
		}
		pos += len(line) + 1
	}
	return len(lines) - 1
}

func formatWhitespaceHint(contentLines []string, startLine, endLine int) string {
	var b strings.Builder
	b.WriteString("A whitespace-normalized match was found, so the text exists but with different whitespace (tabs vs spaces, or different indentation).\n")
	fmt.Fprintf(&b, "Actual file content (lines %d-%d):\n", startLine+1, endLine+1)
	for i := startLine; i <= endLine && i < len(contentLines); i++ {
		fmt.Fprintf(&b, "%6d|%s\n", i+1, visualizeWS(contentLines[i]))
	}
	b.WriteString("Use the exact whitespace shown above (→ = tab, · = space).")
	return b.String()
}

// diagnoseBestLineMatch finds the line window that best matches oldLines after
// trimming, and reports it when at least half the lines agree.
func diagnoseBestLineMatch(contentLines, oldLines []string) string {
	if len(oldLines) == 0 || len(contentLines) == 0 {
		return ""
	}
	trimmedOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		trimmedOld[i] = strings.TrimSpace(l)
	}
	for len(trimmedOld) > 0 && trimmedOld[0] == "" {
		trimmedOld = trimmedOld[1:]
	}
	for len(trimmedOld) > 0 && trimmedOld[len(trimmedOld)-1] == "" {
		trimmedOld = trimmedOld[:len(trimmedOld)-1]
	}
	if len(trimmedOld) == 0 {
		return ""
	}

	bestScore, bestStart := 0, -1
	window := len(trimmedOld)
	for start := range len(contentLines) - window + 1 {
		score := 0
		for j := range window {
			if strings.TrimSpace(contentLines[start+j]) == trimmedOld[j] {
				score++
			}
		}
		if score > bestScore {
			bestScore, bestStart = score, start
		}
	}
	if bestStart == -1 || bestScore < (window+1)/2 {
		return ""
	}

	endLine := bestStart + window - 1
	ctxStart := max(0, bestStart-1)
	ctxEnd := min(len(contentLines)-1, endLine+1)

	var b strings.Builder
	fmt.Fprintf(&b, "No exact match found. Closest match at lines %d-%d (%d/%d lines match after trimming whitespace):\n",
		bestStart+1, endLine+1, bestScore, window)
	for i := ctxStart; i <= ctxEnd; i++ {
		fmt.Fprintf(&b, "%6d|%s\n", i+1, visualizeWS(contentLines[i]))
	}
	b.WriteString("Use the exact text shown above (→ = tab, · = space).")
	return b.String()
}

// visualizeWS makes tabs and leading spaces visible.
func visualizeWS(s string) string {
	s = strings.ReplaceAll(s, "\t", "→")
	trimmed := strings.TrimLeft(s, " ")
	if leading := len(s) - len(trimmed); leading > 0 {
		s = strings.Repeat("·", leading) + trimmed
	}
	return s
}
