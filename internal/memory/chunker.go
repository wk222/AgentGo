package memory

import (
	"strings"
	"unicode/utf8"
)

// ChunkContent splits a large text content into semantic chunks.
//
// maxChunkSize is measured in bytes, not characters: a Chinese character counts
// as three, so for Chinese text 1200 means roughly 400 characters. overlap is
// accepted so the function fits ChunkFunc and is not used: pieces end at header,
// paragraph or sentence boundaries. Use ChunkContentRunes to measure in
// characters.
func ChunkContent(content string, maxChunkSize int, overlap int) []string {
	return chunkBy(content, maxChunkSize, byteLen)
}

// ChunkContentRunes is ChunkContent with maxChunkSize measured in characters
// (runes), which is the unit the document chunker in internal/docchunk uses.
// Everything else is identical, so comparing the two isolates the unit.
func ChunkContentRunes(content string, maxChunkSize int, overlap int) []string {
	return chunkBy(content, maxChunkSize, utf8.RuneCountInString)
}

func byteLen(s string) int { return len(s) }

// chunkBuf is a strings.Builder that also keeps its length in the caller's unit.
type chunkBuf struct {
	b    strings.Builder
	n    int
	size func(string) int
}

func (c *chunkBuf) Len() int { return c.n }

func (c *chunkBuf) WriteString(s string) {
	c.b.WriteString(s)
	c.n += c.size(s)
}

func (c *chunkBuf) Reset() {
	c.b.Reset()
	c.n = 0
}

func (c *chunkBuf) String() string { return c.b.String() }

// chunkBy is the splitter. size measures a string in whatever unit maxChunkSize
// is in; it must be additive over concatenation, as byte and rune counts are.
func chunkBy(content string, maxChunkSize int, size func(string) int) []string {
	content = strings.TrimSpace(content)
	if size(content) <= maxChunkSize {
		return []string{content}
	}

	// 1. Split by markdown headers (semantic header chunking)
	sections := splitByHeaders(content)
	var finalChunks []string

	for _, sec := range sections {
		if size(sec) <= maxChunkSize {
			finalChunks = append(finalChunks, sec)
			continue
		}

		// 2. Split by paragraphs
		paras := strings.Split(sec, "\n\n")
		currentChunk := chunkBuf{size: size}

		for _, para := range paras {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}

			if currentChunk.Len()+size(para)+2 <= maxChunkSize {
				if currentChunk.Len() > 0 {
					currentChunk.WriteString("\n\n")
				}
				currentChunk.WriteString(para)
			} else {
				if currentChunk.Len() > 0 {
					finalChunks = append(finalChunks, currentChunk.String())
					currentChunk.Reset()
				}

				if size(para) <= maxChunkSize {
					currentChunk.WriteString(para)
				} else {
					// 3. Split by sentences if a single paragraph is too large
					sentences := splitBySentences(para)
					for _, sent := range sentences {
						sent = strings.TrimSpace(sent)
						if sent == "" {
							continue
						}
						if currentChunk.Len()+size(sent)+1 <= maxChunkSize {
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

func splitByHeaders(text string) []string {
	lines := strings.Split(text, "\n")
	var sections []string
	var currentSection strings.Builder

	for _, line := range lines {
		// Detect markdown header
		if strings.HasPrefix(line, "#") {
			if currentSection.Len() > 0 {
				sections = append(sections, currentSection.String())
				currentSection.Reset()
			}
		}
		if currentSection.Len() > 0 {
			currentSection.WriteString("\n")
		}
		currentSection.WriteString(line)
	}
	if currentSection.Len() > 0 {
		sections = append(sections, currentSection.String())
	}
	return sections
}

func splitBySentences(text string) []string {
	runes := []rune(text)
	var sentences []string
	var start int
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '.' || c == '?' || c == '!' || c == '。' || c == '？' || c == '！' {
			if i+1 == len(runes) || runes[i+1] == ' ' || runes[i+1] == '\n' || runes[i+1] == '\r' || runes[i+1] == '\u3000' {
				sentences = append(sentences, string(runes[start:i+1]))
				start = i + 1
			}
		}
	}
	if start < len(runes) {
		sentences = append(sentences, string(runes[start:]))
	}
	return sentences
}
