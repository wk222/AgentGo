// Derived from WeKnora (github.com/Tencent/WeKnora), MIT.
// See licenses/weknora/NOTICE.md.
//
// This is WeKnora's internal/application/service/ocr_sanitizer.go with its two
// external dependencies dropped: the HTML-to-Markdown library is not in
// AgentGo's dependency set, so HTML is unwrapped with the standard library
// instead. That loses fidelity for HTML tables, which is acceptable because the
// providers here are asked for plain text or Markdown in the first place.
//
// Why validate at all: the text goes into a model prompt and, when the caller
// indexes it, into the chunk store. A vision model that falls into a decoding
// loop produces a long, plausible, useless block; trimming it is not enough,
// because the plausible prefix is exactly what would get indexed.

package visionocr

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

var (
	htmlTagPattern    = regexp.MustCompile(`<[^>]+>`)
	codeBlockPattern  = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*\n(.*?)\n\\s*```\\s*$")
	htmlDocPattern    = regexp.MustCompile(`(?i)^\s*(<\!DOCTYPE|<html|<body|<div|<p[\s>]|<table|<h[1-6][\s>])`)
	multipleNewlines  = regexp.MustCompile(`\n{3,}`)
	tableRowPattern   = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr\s*>`)
	tableCellPattern  = regexp.MustCompile(`(?is)<t[hd]\b[^>]*>(.*?)</t[hd]\s*>`)
	tableBlockPattern = regexp.MustCompile(`(?is)<table\b[^>]*>.*?</table\s*>`)

	// knownEmptyReplies are what vision models say when an image has no text.
	// Compared after lowercasing and stripping trailing punctuation.
	knownEmptyReplies = []string{
		"无文字内容",
		"无法识别",
		"图片中没有文字",
		"图片中没有可识别的文字",
		"no text",
		"no text content",
		"no content",
		"empty",
	}
)

// ErrUnusableReason reports why validated OCR text was rejected. Callers that
// only want the text can ignore it; callers that log should report it, because
// "the model returned nothing" and "the model went into a loop" mean different
// things to whoever is tuning the prompt.
type ErrUnusableReason struct{ Reason string }

func (e *ErrUnusableReason) Error() string {
	return fmt.Sprintf("visionocr: unusable OCR output: %s", e.Reason)
}

// IsNoText reports whether err means the provider saw no text in the image,
// which is a legitimate outcome rather than a failure worth retrying.
func IsNoText(err error) bool {
	var r *ErrUnusableReason
	if !errors.As(err, &r) {
		return false
	}
	return r.Reason == "empty_content"
}

// Validate cleans up raw provider output and rejects what must not be indexed.
// A nil error with empty text means the provider explicitly reported no text.
func Validate(raw string) (string, error) { return validateOCRText(raw) }

// Sanitize is Validate for callers that want the text and not the reason.
func Sanitize(raw string) string {
	text, _ := validateOCRText(raw)
	return text
}

// validateOCRText distinguishes an explicit no-text answer from unusable model
// output. It rejects the entire response rather than keeping a plausible prefix
// followed by hallucinated repetition.
func validateOCRText(raw string) (string, error) {
	text := normalizeOCRText(raw)
	if isKnownEmptyReply(text) {
		return "", nil
	}
	if text == "" {
		return "", &ErrUnusableReason{Reason: "empty_content"}
	}
	readable := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) ||
			(unicode.IsSymbol(r) && !strings.ContainsRune("|~`", r)) {
			readable = true
			break
		}
	}
	if !readable {
		return "", &ErrUnusableReason{Reason: "no_readable_content"}
	}
	if hasOCRRepetition(text) {
		return "", &ErrUnusableReason{Reason: "repetitive_content"}
	}
	return text, nil
}

// hasOCRRepetition looks for the decoding loop a model falls into when it
// cannot read an image: a long run of prose with a short period.
//
// Markdown table rows are removed first. A table legitimately repeats its shape
// row after row — forty identical expense lines are data, not a loop — and a
// detector that cannot tell those apart would reject exactly the documents OCR
// is most useful for. Looping inside a table is left undetected on purpose:
// rejecting real tables is the worse error.
func hasOCRRepetition(text string) bool {
	runes := []rune(strings.Join(strings.Fields(withoutTableRows(text)), " "))
	if len(runes) < 512 {
		return false
	}
	coverage := make([]int, len(runes)+1)
	for period := 1; period <= 64 && period <= len(runes)/32; period++ {
		matched := 0
		recordRun := func(end int) {
			span := matched + period
			if span >= 512 && span >= period*32 {
				coverage[end-span]++
				coverage[end]--
			}
		}
		for i := period; i < len(runes); i++ {
			if runes[i] == runes[i-period] {
				matched++
			} else {
				recordRun(i)
				matched = 0
			}
		}
		recordRun(len(runes))
	}
	active, repeated := 0, 0
	for i := range runes {
		active += coverage[i]
		if active > 0 {
			repeated++
		}
	}
	return repeated*5 >= len(runes)*4
}

// withoutTableRows drops Markdown table lines (rows and separator rows) so that
// repetition detection only judges prose.
func withoutTableRows(text string) string {
	if !strings.Contains(text, "|") {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func normalizeOCRText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	text = stripMarkdownCodeBlock(text)

	// Convert tables first, before any HTML unwrapping. Order matters: unwrapping
	// an HTML document drops the <table> tags, after which the row structure is
	// gone and a chunker will happily split inside a row.
	text = tablesToMarkdown(text)

	// If stripping tags leaves almost nothing, the response is useless (e.g.
	// "<html><body><div class=\"image\"><img/></div></body></html>"). This runs
	// after table conversion so a short but genuine table is not discarded.
	if htmlTagPattern.MatchString(text) {
		plainText := strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(text, "")))
		// Preserve no-text answers before a short response is discarded.
		if isKnownEmptyReply(plainText) {
			return plainText
		}
		if len(plainText) < 10 {
			return ""
		}
	}

	if looksLikeHTML(text) {
		text = strings.TrimSpace(htmlToText(text))
		if text == "" {
			return ""
		}
	}

	text = multipleNewlines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// stripMarkdownCodeBlock removes a code-fence wrapper that models add around
// their output (e.g. ```html\n...\n```).
func stripMarkdownCodeBlock(text string) string {
	if m := codeBlockPattern.FindStringSubmatch(text); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return text
}

// looksLikeHTML reports whether the text is an HTML document or is dominated by
// tags.
func looksLikeHTML(text string) bool {
	if htmlDocPattern.MatchString(text) {
		return true
	}
	tags := htmlTagPattern.FindAllString(text, -1)
	if len(tags) == 0 {
		return false
	}
	tagChars := 0
	for _, t := range tags {
		tagChars += len(t)
	}
	return float64(tagChars)/float64(len(text)) > 0.3
}

// blockTags become line breaks so unwrapped text keeps its reading order.
var blockTags = regexp.MustCompile(`(?i)</?(?:p|div|section|article|header|footer|li|ul|ol|blockquote|pre|h[1-6]|tr|br)\b[^>]*>`)

// htmlToText unwraps HTML to plain text. It is a lossy substitute for a real
// HTML-to-Markdown converter: block boundaries become newlines and inline markup
// is dropped. Tables are handled separately by tablesToMarkdown so their
// structure survives.
func htmlToText(content string) string {
	out := blockTags.ReplaceAllString(content, "\n")
	out = htmlTagPattern.ReplaceAllString(out, "")
	out = html.UnescapeString(out)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// tablesToMarkdown rewrites each <table> as a GFM table so a chunk boundary does
// not fall inside a row. It runs unconditionally and is a no-op when there is no
// table.
func tablesToMarkdown(content string) string {
	if !strings.Contains(strings.ToLower(content), "<table") {
		return content
	}
	return tableBlockPattern.ReplaceAllStringFunc(content, func(block string) string {
		var rows [][]string
		for _, row := range tableRowPattern.FindAllStringSubmatch(block, -1) {
			var cells []string
			for _, cell := range tableCellPattern.FindAllStringSubmatch(row[1], -1) {
				text := htmlTagPattern.ReplaceAllString(cell[1], " ")
				text = html.UnescapeString(text)
				text = strings.Join(strings.Fields(text), " ")
				text = strings.ReplaceAll(text, "|", `\|`)
				cells = append(cells, strings.TrimSpace(text))
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
		}
		if len(rows) == 0 {
			return block
		}
		width := 0
		for _, r := range rows {
			if len(r) > width {
				width = len(r)
			}
		}
		var sb strings.Builder
		for i, r := range rows {
			for len(r) < width {
				r = append(r, "")
			}
			sb.WriteString("| " + strings.Join(r, " | ") + " |\n")
			if i == 0 {
				sep := make([]string, width)
				for j := range sep {
					sep[j] = "---"
				}
				sb.WriteString("| " + strings.Join(sep, " | ") + " |\n")
			}
		}
		return sb.String()
	})
}

// isKnownEmptyReply reports whether the text is a known "no content" reply.
// Trailing punctuation is stripped so "No text content." still matches.
func isKnownEmptyReply(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = strings.TrimRight(lower, ".!?。！？")
	for _, phrase := range knownEmptyReplies {
		if lower == strings.ToLower(phrase) {
			return true
		}
	}
	return false
}
