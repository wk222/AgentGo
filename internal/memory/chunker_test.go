package memory

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
)

// refChunkContent is ChunkContent exactly as it was before chunkBy was split
// out of it, kept so the tests can show that the byte-measured splitter still
// behaves identically.
func refChunkContent(content string, maxChunkSize int, overlap int) []string {
	content = strings.TrimSpace(content)
	if len(content) <= maxChunkSize {
		return []string{content}
	}

	// 1. Split by markdown headers (semantic header chunking)
	sections := splitByHeaders(content)
	var finalChunks []string

	for _, sec := range sections {
		if len(sec) <= maxChunkSize {
			finalChunks = append(finalChunks, sec)
			continue
		}

		// 2. Split by paragraphs
		paras := strings.Split(sec, "\n\n")
		var currentChunk strings.Builder

		for _, para := range paras {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}

			if currentChunk.Len()+len(para)+2 <= maxChunkSize {
				if currentChunk.Len() > 0 {
					currentChunk.WriteString("\n\n")
				}
				currentChunk.WriteString(para)
			} else {
				if currentChunk.Len() > 0 {
					finalChunks = append(finalChunks, currentChunk.String())
					currentChunk.Reset()
				}

				if len(para) <= maxChunkSize {
					currentChunk.WriteString(para)
				} else {
					// 3. Split by sentences if a single paragraph is too large
					sentences := splitBySentences(para)
					for _, sent := range sentences {
						sent = strings.TrimSpace(sent)
						if sent == "" {
							continue
						}
						if currentChunk.Len()+len(sent)+1 <= maxChunkSize {
							if currentChunk.Len() > 0 {
								currentChunk.WriteString(" ")
							}
							currentChunk.WriteString(sent)
						} else {
							if currentChunk.Len() > 0 {
								finalChunks = append(finalChunks, currentChunk.String())
								currentChunk.Reset()
							}
							currentChunk.WriteString(sent)
						}
					}
				}
			}
		}
		if currentChunk.Len() > 0 {
			finalChunks = append(finalChunks, currentChunk.String())
		}
	}
	return finalChunks
}

func chunkCorpus() map[string]string {
	para := func(s string, n int) string { return strings.TrimSpace(strings.Repeat(s, n)) }
	return map[string]string{
		"empty":         "",
		"short":         "A short note.",
		"english":       para("The quick brown fox jumps over the lazy dog. ", 80),
		"english paras": strings.Join([]string{para("Alpha beta gamma delta. ", 30), para("Epsilon zeta eta theta. ", 30), para("Iota kappa lambda mu. ", 30)}, "\n\n"),
		"headers": "# Title\n\n" + para("Intro sentence here. ", 20) + "\n\n## Part one\n\n" + para("Body of part one. ", 40) +
			"\n\n## Part two\n\n" + para("Body of part two. ", 40),
		"chinese paras":                 strings.Join([]string{para("合同金额以双方签字确认为准。", 20), para("付款方式为验收后三十日内支付。", 20), para("违约责任按照本协议第七条执行。", 20)}, "\n\n"),
		"chinese sentences with spaces": para("这是第一句。 这是第二句。 这是第三句。 ", 40),
		"mixed":                         "# 采购申请 Purchase request\n\n" + para("申请部门：行政部 Department: Admin. ", 15) + "\n\n" + para("Items listed below 明细如下。 ", 30),
		"one long sentence":             para("word ", 400),
	}
}

func TestChunkContentBehaviourIsUnchanged(t *testing.T) {
	for name, text := range chunkCorpus() {
		for _, size := range []int{20, 100, 400, 1200} {
			t.Run(fmt.Sprintf("%s/%d", name, size), func(t *testing.T) {
				got, want := ChunkContent(text, size, 200), refChunkContent(text, size, 200)
				if len(got) != len(want) {
					t.Fatalf("%d chunks, reference has %d", len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("chunk %d differs\n got: %q\nwant: %q", i, got[i], want[i])
					}
				}
			})
		}
	}
}

func TestChunkContentRunesEqualsBytesOnASCII(t *testing.T) {
	for _, name := range []string{"short", "english", "english paras", "headers", "one long sentence"} {
		text := chunkCorpus()[name]
		for _, size := range []int{20, 100, 400} {
			a, b := ChunkContent(text, size, 0), ChunkContentRunes(text, size, 0)
			if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
				t.Fatalf("%s/%d: one byte is one rune in ASCII, so the two units must split identically", name, size)
			}
		}
	}
}

// The reason the rune variant exists: a size of 1200 means 1200 characters in
// one and about 400 in the other when the text is Chinese.
func TestChunkContentRunesMeasuresCharacters(t *testing.T) {
	var paras []string
	for i := 0; i < 30; i++ {
		paras = append(paras, strings.Repeat(fmt.Sprintf("第%d段合同条款内容", i), 12)) // about 100 characters
	}
	text := strings.Join(paras, "\n\n")

	byBytes, byRunes := ChunkContent(text, 1200, 0), ChunkContentRunes(text, 1200, 0)
	for i, c := range byRunes {
		if n := len([]rune(c)); n > 1200 {
			t.Fatalf("rune chunk %d has %d characters, limit 1200", i, n)
		}
	}
	for i, c := range byBytes {
		if len(c) > 1200 {
			t.Fatalf("byte chunk %d has %d bytes, limit 1200", i, len(c))
		}
	}
	if len(byBytes) < 2*len(byRunes) {
		t.Fatalf("chunks by bytes = %d, by runes = %d; the byte limit should cut Chinese about three times finer", len(byBytes), len(byRunes))
	}
}

func TestChunkContentRunesKeepsAllText(t *testing.T) {
	strip := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	for name, text := range chunkCorpus() {
		for _, size := range []int{20, 100, 400} {
			got := strip(strings.Join(ChunkContentRunes(text, size, 0), ""))
			if want := strip(text); got != want {
				t.Fatalf("%s/%d: text was lost or reordered\n got: %q\nwant: %q", name, size, got, want)
			}
		}
	}
}
