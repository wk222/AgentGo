package docchunk

import (
	"strings"
	"testing"
)

// These tests pin the behaviour memory actually depends on when the adaptive
// chunker is mounted. The ported splitter tests cover the algorithm; these cover
// the adapter in service.go, which decides what memory stores.

// chunkerDoc is a document whose sections are longer than one chunk, so both the
// section boundaries and the breadcrumbs matter.
func chunkerDoc() string {
	body := strings.Repeat("这是概述段落的正文内容，用于测试切分效果。", 20)
	bg := strings.Repeat("背景说明的文字，需要足够长。", 20)
	goal := strings.Repeat("目标描述的正文内容。", 25)
	risk := strings.Repeat("风险敞口的说明文字，涉及汇率与利率。", 20)
	return "# 第一章 概述\n\n" + body + "\n\n" +
		"## 1.1 背景\n\n" + bg + "\n\n" +
		"## 1.2 目标\n\n" + goal + "\n\n" +
		"# 第二章 风险\n\n" + risk + "\n"
}

// Every chunk of a headed document must carry the section path it came from.
// Without it a chunk that says only "7 天内可申请" is much harder to find by
// full-text recall than one that also names its section.
func TestServiceChunksCarryHeadingBreadcrumb(t *testing.T) {
	svc := NewService(StrategyAuto)
	chunks := svc.Chunk(chunkerDoc(), 512, 80)
	if len(chunks) < 2 {
		t.Fatalf("expected the document to split, got %d chunk(s)", len(chunks))
	}
	withBreadcrumb := 0
	for _, c := range chunks {
		if strings.Contains(c, "第一章") || strings.Contains(c, "第二章") {
			withBreadcrumb++
		}
	}
	if withBreadcrumb != len(chunks) {
		t.Errorf("%d of %d chunks have no heading breadcrumb", len(chunks)-withBreadcrumb, len(chunks))
	}
}

// A breadcrumb is context, not content: it must not swallow the budget. Before
// the splitter was ported, memory capped chunks at maxChunkSize runes; a
// breadcrumb prepended afterwards could push a chunk past the embedding limit
// without anyone noticing.
func TestServiceRespectsChunkSizeBudget(t *testing.T) {
	const max = 512
	svc := NewService(StrategyAuto)
	for i, c := range svc.Chunk(chunkerDoc(), max, 80) {
		// The splitter sizes Content against the budget and keeps the header
		// separate; the adapter joins them. Allow the header's own length on top,
		// but nothing more -- a runaway chunk means the budget was not honoured.
		if got := len([]rune(c)); got > max+200 {
			t.Errorf("chunk %d is %d runes, far beyond the %d budget", i, got, max)
		}
	}
}

// Short memories must not be split, and must not gain a breadcrumb either: they
// are stored verbatim and should come back verbatim.
func TestServiceKeepsShortContentIntact(t *testing.T) {
	svc := NewService(StrategyAuto)
	const short = "退货政策：7 天内可申请。"
	got := svc.Chunk(short, 512, 80)
	if len(got) != 1 || got[0] != short {
		t.Fatalf("short content was rewritten: %q", got)
	}
}

// An unknown strategy falls back to auto rather than failing: a typo in an env
// var should not take document ingest down.
func TestServiceFallsBackToAutoStrategy(t *testing.T) {
	for _, in := range []string{"", "nonsense", "AUTO", " heading "} {
		svc := NewService(in)
		switch strings.TrimSpace(strings.ToLower(in)) {
		case "heading":
			if svc.Strategy() != StrategyHeading {
				t.Errorf("NewService(%q).Strategy() = %q, want heading", in, svc.Strategy())
			}
		default:
			if svc.Strategy() != StrategyAuto {
				t.Errorf("NewService(%q).Strategy() = %q, want auto", in, svc.Strategy())
			}
		}
	}
}

// Empty input yields no chunks rather than one empty chunk: memory would
// otherwise store a record with empty content.
func TestServiceReturnsNothingForBlankContent(t *testing.T) {
	svc := NewService(StrategyAuto)
	for _, in := range []string{"", "   ", "\n\n\t"} {
		if got := svc.Chunk(in, 512, 80); len(got) != 0 {
			t.Errorf("Chunk(%q) = %q, want no chunks", in, got)
		}
	}
}

// A nil *Service is reachable if a plugin fails half-way; it must behave like the
// default instead of panicking during ingest.
func TestNilServiceUsesDefaults(t *testing.T) {
	var svc *Service
	if got := svc.Strategy(); got != StrategyAuto {
		t.Errorf("nil Service strategy = %q, want auto", got)
	}
	if got := svc.Chunk("段落。\n\n段落。", 512, 80); len(got) == 0 {
		t.Error("nil Service returned no chunks")
	}
}
