package tools

import (
	"agentgo/internal/evidence"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParserVersion changes when the text/position contract changes. File hashes
// alone do not identify offsets produced by different parser versions.
const parsedFileVersion = "agentgo-parse-v2"

const (
	parsedFileDefaultLimit = 8000
	parsedFileMaxLimit     = 32000
)

type parsedFileSource struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	ParserVersion string `json:"parser_version"`
	Revision      string `json:"revision"`
}

// Start/End are half-open rune ranges in the complete parsed text, even when
// only a window is returned. Mapping states the precision, never a guessed page.
type parsedSourceSpan = evidence.Span

type parsedTextBuilder struct {
	strings.Builder
	chars int
	spans []parsedSourceSpan
}

func (b *parsedTextBuilder) append(text string, span parsedSourceSpan) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
		b.chars++
	}
	span.Start = b.chars
	b.WriteString(text)
	b.chars += charCount(text)
	span.End = b.chars
	b.spans = append(b.spans, span)
}

func fileSHA256(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func readParsedFile(ctx context.Context, root string, in parseFileInput) (parseFileOutput, error) {
	return readParsedFileWithChunker(ctx, root, in, nil)
}

func readParsedFileWithChunker(ctx context.Context, root string, in parseFileInput, chunker DocumentChunker) (parseFileOutput, error) {
	if in.RelativePath == "" {
		return parseFileOutput{}, fmt.Errorf("relative_path is required")
	}
	if in.Offset < 0 || in.Limit < 0 {
		return parseFileOutput{}, fmt.Errorf("offset and limit must be non-negative")
	}
	if in.Limit > parsedFileMaxLimit {
		return parseFileOutput{}, fmt.Errorf("limit must not exceed %d", parsedFileMaxLimit)
	}
	if in.Limit == 0 {
		in.Limit = parsedFileDefaultLimit
	}
	if in.PreviewChunks && in.ChunkSize != 0 && (in.ChunkSize < 128 || in.ChunkSize > 2048) {
		return parseFileOutput{}, fmt.Errorf("chunk_size must be between 128 and 2048")
	}
	out, err := loadParsedFile(ctx, root, in.RelativePath, in.ExpectedSHA256, in.ExpectedRevision)
	if err != nil {
		return parseFileOutput{}, err
	}
	window, err := parsedFileWindow(out, in.Offset, in.Limit)
	if err != nil {
		return parseFileOutput{}, err
	}
	if in.PreviewChunks {
		if chunker == nil {
			window.ChunkPreview = &parsedChunkPreview{Status: "unavailable"}
		} else {
			size := in.ChunkSize
			if size == 0 {
				size = 512
			}
			chunks := chunker(out.Text, size)
			preview, err := makeParsedChunkPreview(out, chunks, in.Offset, in.Offset+window.CharCount)
			if err != nil {
				return parseFileOutput{}, err
			}
			window.ChunkPreview = &preview
		}
	}
	window.Citation = registerEvidence(ctx, EvidenceCitation{Kind: "file", Source: window.Source.Path, Revision: window.Source.Revision, Start: window.Offset, End: window.Offset + window.CharCount, Spans: window.Spans}, window.Text)
	return window, nil
}

func parsedFileWindow(out parseFileOutput, offset, limit int) (parseFileOutput, error) {
	runes := []rune(out.Text)
	if offset < 0 || limit < 0 || offset > len(runes) {
		return parseFileOutput{}, fmt.Errorf("invalid parsed text window: offset=%d limit=%d total=%d", offset, limit, len(runes))
	}
	end := len(runes)
	if limit > 0 && limit < end-offset {
		end = offset + limit
	}
	spans := make([]parsedSourceSpan, 0)
	for _, span := range out.Spans {
		if span.End > offset && span.Start < end {
			spans = append(spans, span)
		}
	}
	out.Text, out.Spans = string(runes[offset:end]), spans
	out.Offset, out.CharCount, out.TotalChars = offset, end-offset, len(runes)
	out.NextOffset = nil
	if end < len(runes) {
		out.NextOffset = &end
	}
	return out, nil
}

// loadParsedFile reads and parses one workspace file in full and returns the
// whole parsed text with its source identity (hash, parser version, revision).
// parse_file windows it; the document index chunks it. Sharing this keeps a
// revision recorded by the index identical to the one parse_file reports, so a
// citation from either resolves against the same snapshot.
func loadParsedFile(ctx context.Context, root, relativePath, expectedSHA, expectedRevision string) (parseFileOutput, error) {
	_, abs, err := resolveWorkspacePath(root, relativePath, false, true)
	if err != nil {
		return parseFileOutput{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("read file: %w", err)
	}
	hash := fileSHA256(data)
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, hash) {
		return parseFileOutput{}, fmt.Errorf("source_changed: file revision differs; restart at offset 0 without expected_sha256")
	}
	if err := ctx.Err(); err != nil {
		return parseFileOutput{}, err
	}
	ext := strings.ToLower(filepath.Ext(abs))
	out, custom, err := tryCustomFileParser(ctx, root, abs, ext)
	if err != nil {
		return parseFileOutput{}, err
	}
	version := parsedFileVersion
	if custom {
		// Commands read the live path rather than the captured bytes. Never attach
		// the earlier hash if the file changed while the command was executing.
		after, err := os.ReadFile(abs)
		if err != nil {
			return parseFileOutput{}, err
		}
		if fileSHA256(after) != hash {
			return parseFileOutput{}, fmt.Errorf("source_changed: file changed during custom parsing")
		}
		version += "/custom/" + out.FileType
		out.Spans = []parsedSourceSpan{{Type: "text", End: charCount(out.Text), Mapping: "parsed_text_only"}}
	} else {
		switch ext {
		case ".pdf":
			out, err = parsePDF(ctx, data)
		case ".docx":
			out, err = parseDOCX(ctx, data)
		case ".xlsx":
			out, err = parseXLSX(ctx, data)
		default:
			out = parsePlainText(data, strings.TrimPrefix(ext, "."))
		}
		if err != nil {
			return parseFileOutput{}, err
		}
	}
	// Preserve the caller's authorized path; do not disclose an unrelated
	// absolute workspace path to the model.
	revision := fileSHA256([]byte(hash + "\x00" + version + "\x00" + out.Text))
	if expectedRevision != "" && !strings.EqualFold(expectedRevision, revision) {
		return parseFileOutput{}, fmt.Errorf("source_changed: source or parser output differs; restart at offset 0 without expected_revision")
	}
	out.Source = &parsedFileSource{Path: filepath.ToSlash(filepath.Clean(relativePath)), SHA256: hash, ParserVersion: version, Revision: revision}
	return out, nil
}
