package docchunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeadingContextsRespectSourceOffsetsAndFences(t *testing.T) {
	text := "前言🙂\n# 第一章\n正文甲\n```md\n# 假标题\n```\n## 条款\n正文乙\n# 第二章\n正文丙"
	offset := func(fragment string) int { return utf8.RuneCountInString(text[:strings.Index(text, fragment)]) }
	got := HeadingContexts(text, []int{0, offset("正文甲"), offset("# 假标题"), offset("正文乙"), offset("正文丙")})
	want := []string{"", "# 第一章", "# 第一章", "# 第一章\n## 条款", "# 第二章"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("context at %d = %q, want %q", i, got[i], want[i])
		}
	}
}
