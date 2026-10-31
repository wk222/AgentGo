package coderuntime

// Find-and-replace for the edit tools.
//
// Strategy: take the text literally if it occurs; otherwise walk a ladder of
// progressively looser line comparisons (trailing whitespace, edge whitespace,
// inner whitespace runs, typographic punctuation) and use the first rung that
// finds the block. Every rung insists on a single location unless replace_all
// is set, and a looser rung is never consulted once a stricter one has hits.
// Text is never matched by similarity score; scores only feed the error hint
// (edit_hint.go).
//
// Independent implementation. The idea of graded line comparison is common to
// several open-source agents (OpenAI Codex CLI, Aider, Gemini CLI; all
// Apache-2.0) and was studied for this design; no code is copied.

import (
	"errors"
	"slices"
	"strings"
)

// tolerance is one rung of the ladder: lines are equal when their keys are.
type tolerance struct {
	label string
	key   func(string) string
}

var ladder = []tolerance{
	{"trailing whitespace", func(s string) string { return strings.TrimRight(s, " \t\r") }},
	{"indentation or line-edge whitespace", strings.TrimSpace},
	{"whitespace runs inside lines", collapseSpace},
	{"typographic punctuation (curly quotes, dashes)", func(s string) string {
		return asciiPunctuation.Replace(collapseSpace(s))
	}},
}

var asciiPunctuation = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
	"\u201C", `"`, "\u201D", `"`, "\u201E", `"`, "\u201F", `"`,
	"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
	"\u2026", "...",
)

// collapseSpace trims a line and folds every whitespace run (NBSP included)
// into one space.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// editApplied is the outcome of a successful applyEdit.
type editApplied struct {
	Content string
	Rung    int // 0 = literal match; n > 0 = ladder[n-1]
	Count   int // replaced locations
}

var errEmptyOld = errors.New("old_string is empty")

var errAmbiguousLiteral = errors.New("old_string occurs in more than one place. Add neighbouring lines so only one place matches, or set replace_all to true")

// applyEdit replaces oldStr with newStr in content (LF line endings).
func applyEdit(content, oldStr, newStr string, replaceAll bool) (editApplied, error) {
	if oldStr == "" {
		return editApplied{}, errEmptyOld
	}
	if first := strings.Index(content, oldStr); first >= 0 {
		if !replaceAll && first != strings.LastIndex(content, oldStr) {
			return editApplied{}, errAmbiguousLiteral
		}
		return editApplied{
			Content: strings.ReplaceAll(content, oldStr, newStr),
			Count:   strings.Count(content, oldStr),
		}, nil
	}

	pat, endsWithNL := patternLines(oldStr)
	if !hasText(pat) {
		return editApplied{}, notFoundError(content, oldStr, newStr)
	}
	lines := strings.Split(content, "\n")
	repl := replacementLines(strings.ReplaceAll(newStr, "\r\n", "\n"), endsWithNL)

	for i, rung := range ladder {
		starts := findWindows(lines, pat, rung.key)
		if len(starts) == 0 {
			continue
		}
		if len(starts) > 1 && !replaceAll {
			return editApplied{}, ambiguousTolerantError(len(starts), rung.label)
		}
		return editApplied{
			Content: strings.Join(spliceBlocks(lines, starts, pat, repl), "\n"),
			Rung:    i + 1,
			Count:   len(starts),
		}, nil
	}
	return editApplied{}, notFoundError(content, oldStr, newStr)
}

// patternLines splits oldStr into lines. A trailing newline means "whole
// lines" and is not a line of its own.
func patternLines(s string) (lines []string, endsWithNL bool) {
	lines = strings.Split(s, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1], true
	}
	return lines, false
}

func replacementLines(s string, endsWithNL bool) []string {
	lines := strings.Split(s, "\n")
	if endsWithNL && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hasText(lines []string) bool {
	for _, l := range lines {
		if !isBlank(l) {
			return true
		}
	}
	return false
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// findWindows returns the start line of each non-overlapping run of lines that
// equals pat under key, scanning top to bottom.
func findWindows(lines, pat []string, key func(string) string) []int {
	want := make([]string, len(pat))
	for i, p := range pat {
		want[i] = key(p)
	}
	have := make([]string, len(lines))
	for i, l := range lines {
		have[i] = key(l)
	}
	var starts []int
	for i := 0; i+len(want) <= len(have); {
		if slices.Equal(have[i:i+len(want)], want) {
			starts = append(starts, i)
			i += len(want)
			continue
		}
		i++
	}
	return starts
}

// spliceBlocks swaps each matched block for the replacement, re-indented to
// the block it replaces.
func spliceBlocks(lines []string, starts []int, pat, repl []string) []string {
	fileUnit := indentUnit(lines)
	out := make([]string, 0, len(lines)+len(repl))
	next := 0
	for _, s := range starts {
		out = append(out, lines[next:s]...)
		out = append(out, fitIndent(lines[s:s+len(pat)], pat, repl, fileUnit)...)
		next = s + len(pat)
	}
	return append(out, lines[next:]...)
}

// --- indentation ------------------------------------------------------------

// fitIndent re-indents repl, written against the old_string block pat, so it
// suits the block actual that pat matched in the file. Three cases, cheapest
// first: indentation already agrees; the whole block is shifted by one fixed
// prefix; or the file uses a different indent style, so depths are remapped.
func fitIndent(actual, pat, repl []string, fileUnit string) []string {
	if sameIndentation(actual, pat) {
		return repl
	}
	if out, ok := shiftBlock(actual, pat, repl); ok {
		return out
	}
	return remapDepth(actual, pat, repl, fileUnit)
}

func leadingWS(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }

func sameIndentation(actual, pat []string) bool {
	for i := range pat {
		if !isBlank(pat[i]) && leadingWS(actual[i]) != leadingWS(pat[i]) {
			return false
		}
	}
	return true
}

// commonLead is the longest leading whitespace shared by all non-blank lines.
func commonLead(lines []string) string {
	var lead string
	seen := false
	for _, l := range lines {
		if isBlank(l) {
			continue
		}
		w := leadingWS(l)
		if !seen {
			lead, seen = w, true
			continue
		}
		n := 0
		for n < len(lead) && n < len(w) && lead[n] == w[n] {
			n++
		}
		lead = lead[:n]
	}
	return lead
}

// shiftBlock handles the case where the model's block differs from the file's
// only by one constant indentation prefix. It fails (ok=false) when relative
// indentation differs or a replacement line sits left of the old block.
func shiftBlock(actual, pat, repl []string) (out []string, ok bool) {
	from, to := commonLead(pat), commonLead(actual)
	for i := range pat {
		if isBlank(pat[i]) {
			continue
		}
		a, p := leadingWS(actual[i]), leadingWS(pat[i])
		if len(a) < len(to) || len(p) < len(from) || a[len(to):] != p[len(from):] {
			return nil, false
		}
	}
	out = make([]string, len(repl))
	for i, l := range repl {
		switch {
		case isBlank(l):
			out[i] = l
		case strings.HasPrefix(l, from):
			out[i] = to + l[len(from):]
		default:
			return nil, false
		}
	}
	return out, true
}

// remapDepth converts nesting depth from the model's indent style to the
// file's, anchored so the first non-blank line lands at the matched block's
// depth.
func remapDepth(actual, pat, repl []string, fileUnit string) []string {
	if fileUnit == "" {
		return repl
	}
	srcUnit := indentUnit(pat)
	if srcUnit == "" {
		srcUnit = indentUnit(repl)
	}
	if srcUnit == "" {
		srcUnit = fileUnit
	}
	shift := firstDepth(actual, fileUnit) - firstDepth(pat, srcUnit)
	out := make([]string, len(repl))
	for i, l := range repl {
		if isBlank(l) {
			out[i] = l
			continue
		}
		body := strings.TrimLeft(l, " \t")
		depth := max(depthOf(leadingWS(l), srcUnit)+shift, 0)
		out[i] = strings.Repeat(fileUnit, depth) + body
	}
	return out
}

// indentUnit guesses one indentation step: a tab, or the smallest run of
// leading spaces. It returns "" when no line is indented.
func indentUnit(lines []string) string {
	least := 0
	for _, l := range lines {
		lead := leadingWS(l)
		if lead == "" || isBlank(l) {
			continue
		}
		if strings.Contains(lead, "\t") {
			return "\t"
		}
		if least == 0 || len(lead) < least {
			least = len(lead)
		}
	}
	return strings.Repeat(" ", least)
}

func depthOf(lead, unit string) int {
	switch unit {
	case "":
		return 0
	case "\t":
		return strings.Count(lead, "\t")
	}
	return strings.Count(lead, " ") / len(unit)
}

func firstDepth(lines []string, unit string) int {
	for _, l := range lines {
		if !isBlank(l) {
			return depthOf(leadingWS(l), unit)
		}
	}
	return 0
}
