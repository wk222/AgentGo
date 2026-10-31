package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentChunkPreviewKeepsFullOffsetsAndStableParents(t *testing.T) {
	root := t.TempDir()
	text := "甲乙🙂丙丁戊己"
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	fn := func(string, int) DocumentChunks {
		parent := 0
		return DocumentChunks{Parents: []DocumentChunk{{Start: 0, End: 7, ContextHeader: "章节"}}, Children: []DocumentChunk{{Start: 0, End: 4, ContextHeader: "章节", ParentIndex: &parent}, {Start: 2, End: 7, ContextHeader: "章节", ParentIndex: &parent}}}
	}
	read := func(offset int) parseFileOutput {
		out, err := readParsedFileWithChunker(context.Background(), root, parseFileInput{RelativePath: "source.txt", Offset: offset, Limit: 4, PreviewChunks: true}, fn)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b := read(1), read(2)
	if a.Text != string([]rune(text)[1:5]) || a.ChunkPreview == nil || len(a.ChunkPreview.Children) != 2 || len(a.ChunkPreview.Parents) != 1 {
		t.Fatalf("bad window/preview: %+v", a)
	}
	for i, ch := range a.ChunkPreview.Children {
		if ch.ID != b.ChunkPreview.Children[i].ID || ch.ParentID != a.ChunkPreview.Parents[0].ID {
			t.Fatal("chunk identity or parent relationship drifts between windows")
		}
		if len(ch.Spans) != 1 || ch.Spans[0].Start != ch.Start || ch.Spans[0].End != ch.End {
			t.Fatalf("source overlap wrong: %+v", ch)
		}
	}
	if a.ChunkPreview.Children[0].Start != 0 {
		t.Fatal("chunk was wrongly rebased to the returned window")
	}
}

func TestDocumentChunkPreviewPreservesPDFPageGaps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.pdf"), buildTestPDF([]string{"Alpha contract", "", "Gamma contract"}), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := readParsedFileWithChunker(context.Background(), root, parseFileInput{RelativePath: "source.pdf", PreviewChunks: true}, func(text string, _ int) DocumentChunks {
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: charCount(text)}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := out.ChunkPreview.Children[0].Spans
	if len(spans) != 2 || spans[0].Page != 1 || spans[1].Page != 3 {
		t.Fatalf("chunk lost page mapping: %+v", spans)
	}
}

func TestDocumentChunkPreviewUnavailableAndBadProviderFailExplicitly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("甲乙丙"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := readParsedFile(context.Background(), root, parseFileInput{RelativePath: "a.txt", PreviewChunks: true})
	if err != nil || out.ChunkPreview == nil || out.ChunkPreview.Status != "unavailable" {
		t.Fatalf("missing plugin should be explicit: %+v %v", out, err)
	}
	_, err = readParsedFileWithChunker(context.Background(), root, parseFileInput{RelativePath: "a.txt", PreviewChunks: true}, func(string, int) DocumentChunks {
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: 99}}}
	})
	if err == nil || !strings.Contains(err.Error(), "invalid child") {
		t.Fatalf("bad source range accepted: %v", err)
	}
	_, err = readParsedFileWithChunker(context.Background(), root, parseFileInput{RelativePath: "a.txt", PreviewChunks: true}, func(string, int) DocumentChunks {
		i := 0
		return DocumentChunks{Parents: []DocumentChunk{{Start: 0, End: 1}}, Children: []DocumentChunk{{Start: 0, End: 3, ParentIndex: &i}}}
	})
	if err == nil || !strings.Contains(err.Error(), "invalid parent relationship") {
		t.Fatalf("bad parent containment accepted: %v", err)
	}
}

func TestDocumentChunkerDisposalDoesNotRemoveNewBinding(t *testing.T) {
	r := NewRegistry()
	old := r.SetDocumentChunker(func(string, int) DocumentChunks { return DocumentChunks{} })
	newer := r.SetDocumentChunker(func(string, int) DocumentChunks { return DocumentChunks{} })
	old()
	if r.getDocumentChunker() == nil {
		t.Fatal("old disposal removed newer binding")
	}
	newer()
	if r.getDocumentChunker() != nil {
		t.Fatal("disposed binding resurrected an older one")
	}
}
