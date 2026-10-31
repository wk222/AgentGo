package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestTokenizeKeepsEnglishAsItWasAndAddsPiecesForChinese(t *testing.T) {
	if got := tokenize("  Session  Context goals "); strings.Join(got, "|") != "session|context|goals" {
		t.Fatalf("English words changed: %v", got)
	}
	got := tokenize("出差报销的住宿标准")
	for _, want := range []string{"出差报销的住宿标准", "出差", "差报", "报销", "住宿", "宿标", "标准"} {
		if !contains(got, want) {
			t.Fatalf("%q is missing from %v", want, got)
		}
	}
	for _, noise := range []string{"销的", "的住"} {
		if contains(got, noise) {
			t.Fatalf("piece %q with a function character was kept: %v", noise, got)
		}
	}
	// A word that is already a two-character piece is not listed twice, and mixed text is split by script.
	mixed := tokenize("报表 report口径")
	if strings.Join(mixed, "|") != "报表|report口径|口径" {
		t.Fatalf("mixed query: %v", mixed)
	}
	// A very long run cannot grow the query without bound.
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteRune(rune(0x4e00 + i))
	}
	if n := len(tokenize(b.String())); n > maxCJKPieces+1 {
		t.Fatalf("%d search words from one run", n)
	}
}

func chineseStore(t *testing.T) *Pipeline {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB().Close() })
	rows := []struct {
		content    string
		importance float64
	}{
		{"出差报销单必须在返程后 7 个工作日内提交，超期需部门负责人签字。", 0.5},
		{"住宿标准：地市 400 元/晚，省会 500 元/晚，超标部分自理。", 0.5},
		{"发票抬头必须是公司全称，税号写完整，个人抬头不予报销。", 0.5},
		{"正文用仿宋三号，标题用小标宋二号居中，行距固定 28 磅。", 0.5},
		{"页码居中，用一字线格式，红头文件版头用公司统一模板。", 0.5},
		// The most important memory of the lot, but it only shares a single piece with the question below.
		{"手机资费标准每月调整一次，由综合部发布。", 1.0},
	}
	for i, r := range rows {
		rec := Record{ID: fmt.Sprintf("c%02d", i), Scope: "s", Status: "active", Modality: "fact", Importance: r.importance, Content: r.content}
		if err = s.Ingest(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return NewPipeline(s)
}

// Without an embedding service, a Chinese question used to find nothing: the whole sentence was one search
// word. It must now find the memories that talk about the same things, most relevant first.
func TestChineseQuestionFindsTheMemoriesItIsAbout(t *testing.T) {
	p := chineseStore(t)
	got, err := p.Recall(context.Background(), "出差报销的住宿标准是多少", RecallOptions{Scope: "s", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("found %d memories for a question the store has answers to", len(got))
	}
	top := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !top["c00"] || !top["c01"] {
		var ids []string
		for _, r := range got {
			ids = append(ids, r.ID+":"+r.Content[:12])
		}
		t.Fatalf("the two memories about travel claims and lodging are not first: %v", ids)
	}
	for _, r := range got[:2] {
		if r.ID == "c05" {
			t.Fatalf("an important memory that shares one piece outranked the relevant ones")
		}
	}
	if none, _ := p.Recall(context.Background(), "完全无关的火星基地", RecallOptions{Scope: "s", Limit: 3}); len(none) != 0 {
		t.Fatalf("an unrelated question matched %d memories", len(none))
	}
}
