// Derived from WeKnora (github.com/Tencent/WeKnora), MIT.
// See licenses/weknora/NOTICE.md.
//
// markdown.go carries the Markdown literal-range helpers that SplitText needs
// before it can treat code spans as atomic. WeKnora keeps them in
// internal/infrastructure/docparser/image_resolver.go next to the image
// uploader; here they stand alone so the chunker has no dependency outside the
// standard library.

package docchunk

import (
	"regexp"
	"sort"
	"strings"
)

type markdownCodeLiteralRange struct {
	start int
	end   int
}

// Literal code containers must not be interpreted as image references.
var (
	htmlCodeLiteralPattern = regexp.MustCompile(`(?is)<pre\b[^>]*>.*?</pre\s*>|<code\b[^>]*>.*?</code\s*>`)
	markdownListItemPrefix = regexp.MustCompile(`^(?:[-*+]|[0-9]+[.)])[ \t]`)
	markdownATXHeading     = regexp.MustCompile(`^#{1,6}(?:[ \t]|$)`)
)

// withoutMarkdownCodeLiterals masks fenced and inline code while preserving
// byte offsets, so a caller that matches on the result can still rewrite the
// original by offset.
func withoutMarkdownCodeLiterals(markdown string) string {
	ranges := markdownCodeLiteralRanges(markdown)
	if len(ranges) == 0 {
		return markdown
	}
	masked := []byte(markdown)
	for _, r := range ranges {
		for i := r.start; i < r.end; i++ {
			if masked[i] != '\n' {
				masked[i] = ' '
			}
		}
	}
	return string(masked)
}

// indentedCodeLiteralRanges identifies indented code and list content in one
// forward scan. A list child may have four leading spaces and still contain a
// normal image, while four more spaces beyond its content indent introduce code.
func indentedCodeLiteralRanges(markdown string, blocks []markdownCodeLiteralRange) []markdownCodeLiteralRange {
	var ranges []markdownCodeLiteralRange
	var listContentIndent int
	hasListItem := false
	blankLines := 0
	inParagraph := false

	for lineStart := 0; lineStart < len(markdown); {
		lineEnd := strings.IndexByte(markdown[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(markdown)
		} else {
			lineEnd += lineStart
		}
		line := markdown[lineStart:lineEnd]
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		trimmed := strings.TrimSpace(line)

		// An indented code block cannot interrupt an existing paragraph.
		indentedCode := strings.HasPrefix(line, "\t") || indent >= 4
		if _, inBlock := codeLiteralRangeAt(blocks, lineStart); inBlock {
			inParagraph = false
		} else if indentedCode && !inParagraph && (!hasListItem || blankLines >= 2 || indent >= listContentIndent) {
			ranges = append(ranges, markdownCodeLiteralRange{start: lineStart, end: lineEnd})
		} else {
			inParagraph = trimmed != "" && !markdownATXHeading.MatchString(trimmed)
		}

		if trimmed == "" {
			blankLines++
			if blankLines >= 2 {
				hasListItem = false
			}
		} else {
			if marker := markdownListItemPrefix.FindString(trimmed); marker != "" {
				blankLines = 0
				listContentIndent = indent + len(marker) + 4
				hasListItem = true
			} else if indent == 0 {
				hasListItem = false
			}
		}

		if lineEnd == len(markdown) {
			break
		}
		lineStart = lineEnd + 1
	}
	return ranges
}

func mergeCodeLiteralRanges(ranges []markdownCodeLiteralRange) []markdownCodeLiteralRange {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	merged := make([]markdownCodeLiteralRange, 0, len(ranges))
	for _, span := range ranges {
		if len(merged) > 0 && span.start <= merged[len(merged)-1].end {
			if span.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = span.end
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func markdownCodeLiteralRanges(markdown string) []markdownCodeLiteralRange {
	protected := fencedCodeLiteralRanges(markdown)
	for _, match := range htmlCodeLiteralPattern.FindAllStringIndex(markdown, -1) {
		protected = append(protected, markdownCodeLiteralRange{start: match[0], end: match[1]})
	}
	protected = mergeCodeLiteralRanges(protected)
	protected = append(protected, indentedCodeLiteralRanges(markdown, protected)...)
	protected = mergeCodeLiteralRanges(protected)
	var inline []markdownCodeLiteralRange
	for i := 0; i < len(markdown); {
		if end, ok := codeLiteralRangeAt(protected, i); ok {
			i = end
			continue
		}
		if markdown[i] != '`' || isEscaped(markdown, i) {
			i++
			continue
		}
		runLength := 1
		for i+runLength < len(markdown) && markdown[i+runLength] == '`' {
			runLength++
		}
		closePos := findInlineCodeLiteralClose(markdown, protected, i+runLength, runLength)
		if closePos < 0 {
			i += runLength
			continue
		}
		inline = append(inline, markdownCodeLiteralRange{start: i, end: closePos + runLength})
		i = closePos + runLength
	}
	return mergeCodeLiteralRanges(append(protected, inline...))
}

func fencedCodeLiteralRanges(markdown string) []markdownCodeLiteralRange {
	var ranges []markdownCodeLiteralRange
	var fence byte
	var fenceLength, fenceStart, fenceQuoteDepth int

	for lineStart := 0; lineStart < len(markdown); {
		lineEnd := strings.IndexByte(markdown[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(markdown)
		} else {
			lineEnd += lineStart
		}
		line := markdown[lineStart:lineEnd]
		trimmed := strings.TrimLeft(line, " \t")
		quoteDepth := 0
		for strings.HasPrefix(trimmed, ">") {
			quoteDepth++
			trimmed = strings.TrimLeft(trimmed[1:], " \t")
		}
		// A quoted fence ends with its container, even if it is unclosed.
		if fence != 0 && quoteDepth < fenceQuoteDepth {
			ranges = append(ranges, markdownCodeLiteralRange{start: fenceStart, end: lineStart})
			fence = 0
		}
		if fence == 0 {
			if marker, length := codeFence(trimmed); length > 0 {
				fence, fenceLength, fenceStart, fenceQuoteDepth = marker, length, lineStart, quoteDepth
			}
		} else if marker, length := codeFence(trimmed); quoteDepth == fenceQuoteDepth &&
			marker == fence && length >= fenceLength &&
			strings.TrimSpace(trimmed[length:]) == "" {
			end := lineEnd
			if end < len(markdown) {
				end++
			}
			ranges = append(ranges, markdownCodeLiteralRange{start: fenceStart, end: end})
			fence, fenceLength = 0, 0
		}
		if lineEnd == len(markdown) {
			break
		}
		lineStart = lineEnd + 1
	}
	if fence != 0 {
		ranges = append(ranges, markdownCodeLiteralRange{start: fenceStart, end: len(markdown)})
	}
	return ranges
}

func codeLiteralRangeAt(ranges []markdownCodeLiteralRange, offset int) (int, bool) {
	index := sort.Search(len(ranges), func(i int) bool { return ranges[i].end > offset })
	if index < len(ranges) && ranges[index].start <= offset {
		return ranges[index].end, true
	}
	return 0, false
}

func findInlineCodeLiteralClose(markdown string, ranges []markdownCodeLiteralRange, start, runLength int) int {
	for i := start; i < len(markdown); {
		if end, ok := codeLiteralRangeAt(ranges, i); ok {
			// Inline HTML does not terminate the surrounding Markdown code span.
			if strings.EqualFold(markdown[i:min(i+5, len(markdown))], "<code") {
				i = end
				continue
			}
			return -1
		}
		if markdown[i] == '\n' {
			next := i + 1
			for next < len(markdown) && (markdown[next] == ' ' || markdown[next] == '\t' || markdown[next] == '\r') {
				next++
			}
			if next < len(markdown) && markdown[next] == '\n' {
				return -1
			}
		}
		if markdown[i] != '`' {
			i++
			continue
		}
		end := i
		for end < len(markdown) && markdown[end] == '`' {
			end++
		}
		if end-i == runLength {
			return i
		}
		i = end
	}
	return -1
}

func codeFence(line string) (byte, int) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0
	}
	marker := line[0]
	length := 0
	for length < len(line) && line[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0
	}
	return marker, length
}

// isEscaped reports whether the byte at pos is preceded by an odd number of
// backslashes (i.e. the character itself is escaped).
func isEscaped(s string, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

type inlineRewrite struct {
	start int
	end   int
	value string
}

// The regex match offsets refer to the original Markdown. Build the output
// in one pass instead of copying the entire document on each substitution.
func applyInlineRewrites(markdown string, edits []inlineRewrite) string {
	if len(edits) == 0 {
		return markdown
	}
	var b strings.Builder
	b.Grow(len(markdown))
	cursor := 0
	for _, e := range edits {
		if e.start < cursor {
			continue
		}
		b.WriteString(markdown[cursor:e.start])
		b.WriteString(e.value)
		cursor = e.end
	}
	b.WriteString(markdown[cursor:])
	return b.String()
}

// reLinkedImage matches the nested [![alt](img_url)](link_url) pattern where
// an image is wrapped inside a Markdown link. We unwrap it to just ![alt](img_url)
// so that downstream image-processing regexes only have to handle the flat form.
// The URL groups support one level of balanced parentheses.
var reLinkedImage = regexp.MustCompile(
	`\[!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)\]` + // [![alt](img_url)]
		`\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`, // (link_url) — captured but discarded
)

// UnwrapLinkedImages replaces all [![alt](img_url)](link_url) occurrences in
// the markdown with just ![alt](img_url), stripping the outer link wrapper.
// This should be called before any image-extraction regex so that only the
// flat ![alt](url) form needs to be handled.
func UnwrapLinkedImages(markdown string) string {
	matches := reLinkedImage.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown
	}
	codeRanges := markdownCodeLiteralRanges(markdown)
	var rewrites []inlineRewrite
	for _, match := range matches {
		if _, ok := codeLiteralRangeAt(codeRanges, match[0]); !ok {
			rewrites = append(rewrites, inlineRewrite{
				match[0], match[1], markdown[match[0]+1 : match[5]+1],
			})
		}
	}
	return applyInlineRewrites(markdown, rewrites)
}
