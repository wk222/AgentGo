package promptbudget

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerptBudgetsIncludeMarkerAndUnicode(t *testing.T) {
	for _, text := range []string{strings.Repeat("\u4e2d\u6587\U0001f408", 1000), strings.Repeat("abc/path/file.go ", 1000)} {
		for _, budget := range []int{0, 1, 20, 80, 300, 1000} {
			got := Excerpt("HEAD "+text+" TAIL", budget)
			if Estimate(got) > budget || !utf8.ValidString(got) {
				t.Fatalf("budget=%d estimate=%d", budget, Estimate(got))
			}
			if budget >= 80 && (!strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL")) {
				t.Fatal("lost ends")
			}
		}
	}
}
