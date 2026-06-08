package memory

import (
	"strings"
)

// ChunkContent splits a large text content into semantic chunks.
func ChunkContent(content string, maxChunkSize int, overlap int) []string {
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
