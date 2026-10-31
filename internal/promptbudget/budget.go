// Package promptbudget provides model-independent packing estimates.
// Estimates are not tokenizer counts; the final model-window guard is still required.
package promptbudget

import (
	"strings"
	"unicode"
)

func Estimate(text string) int {
	ascii, wide := 0, 0
	for _, r := range text {
		if r < 128 {
			ascii++
		} else if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			wide++
		} else if r > 0xFFFF {
			wide += 4
		} else {
			wide += 2
		}
	}
	return (ascii+2)/3 + wide
}

// Excerpt preserves both ends and includes the omission marker in its budget.
func Excerpt(text string, budget int) string {
	text = strings.TrimSpace(text)
	if budget <= 0 {
		return ""
	}
	if Estimate(text) <= budget {
		return text
	}
	const marker = " ...[excerpt]... "
	if budget < Estimate(marker)+2 {
		return ""
	}
	runes := []rune(text)
	lo, hi := 0, len(runes)
	for lo < hi {
		n := (lo + hi + 1) / 2
		head := n * 2 / 3
		candidate := string(runes[:head]) + marker + string(runes[len(runes)-(n-head):])
		if Estimate(candidate) <= budget {
			lo = n
		} else {
			hi = n - 1
		}
	}
	head := lo * 2 / 3
	return string(runes[:head]) + marker + string(runes[len(runes)-(lo-head):])
}
