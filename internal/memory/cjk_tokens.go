package memory

import (
	"strings"
	"unicode"
)

// A run of Chinese characters has no spaces to split on, and SQLite's FTS tokenizer
// indexes the whole run between two punctuation marks as one word. A question such
// as "出差报销怎么走" would therefore find nothing in a memory that says
// "出差报销单必须在返程后提交", with or without an embedding service. The search
// words of a query are the words as written plus the overlapping two-character
// pieces of each Chinese run, which is what a LIKE scan can actually find.

// cjkStopChars are single characters that carry no topic. A piece containing one
// ("按公", "销按") is mostly a grammar accident and would only add noise.
const cjkStopChars = "的了是在把按和与及或并对将被让给从到为以其这那个请我你他她它吗呢吧啊"

// maxCJKPieces bounds the pieces of one query, and with it the size of the LIKE
// clause list.
const maxCJKPieces = 48

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func hasHan(s string) bool {
	for _, r := range s {
		if isHan(r) {
			return true
		}
	}
	return false
}

// cjkPieces returns the two-character pieces of the Han runs inside words, without
// repeating a word that is already there or a piece already produced.
func cjkPieces(words []string) []string {
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		seen[w] = true
	}
	var out []string
	var run []rune
	flush := func() {
		for i := 0; i+1 < len(run) && len(out) < maxCJKPieces; i++ {
			if strings.ContainsRune(cjkStopChars, run[i]) || strings.ContainsRune(cjkStopChars, run[i+1]) {
				continue
			}
			if piece := string(run[i : i+2]); !seen[piece] {
				seen[piece] = true
				out = append(out, piece)
			}
		}
		run = run[:0]
	}
	for _, w := range words {
		for _, r := range w {
			if isHan(r) {
				run = append(run, r)
			} else {
				flush()
			}
		}
		flush()
	}
	return out
}
