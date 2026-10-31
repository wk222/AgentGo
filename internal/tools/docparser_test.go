package tools

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// buildTestPDF writes a PDF with one page per element of pages. A non-empty
// element is shown as a line of text; an empty one is a page that draws nothing,
// which is how a scanned page looks to a parser that reads the text layer.
func buildTestPDF(pages []string) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}

	buf.WriteString("%PDF-1.4\n")
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for i, text := range pages {
		stream := ""
		if text != "" {
			stream = fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

func TestParsePDFCountsPagesAndHasNoNoteWhenAllHaveText(t *testing.T) {
	out, err := parsePDF(context.Background(), buildTestPDF([]string{"Alpha contract", "Beta contract", "Gamma contract"}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Pages != 3 {
		t.Fatalf("Pages = %d, want 3 (it used to be 1 whatever the page count)", out.Pages)
	}
	for _, want := range []string{"Alpha contract", "Beta contract", "Gamma contract"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("text lost %q: %q", want, out.Text)
		}
	}
	if out.Note != "" {
		t.Fatalf("unexpected note: %s", out.Note)
	}
}

func TestParsePDFNamesThePagesThatHaveNoText(t *testing.T) {
	out, err := parsePDF(context.Background(), buildTestPDF([]string{"Alpha contract", "", "Gamma contract", ""}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Pages != 4 {
		t.Fatalf("Pages = %d, want 4", out.Pages)
	}
	if !strings.Contains(out.Text, "Alpha contract") || !strings.Contains(out.Text, "Gamma contract") {
		t.Fatalf("text pages were lost: %q", out.Text)
	}
	if !strings.Contains(out.Note, "2 of 4 pages") || !strings.Contains(out.Note, "2, 4") {
		t.Fatalf("note does not name pages 2 and 4: %q", out.Note)
	}
}

// The reason for the note: a scanned PDF parses without error and returns
// nothing, which a model reads as "the document is empty".
func TestParsePDFWithoutAnyTextSaysItIsProbablyAScan(t *testing.T) {
	out, err := parsePDF(context.Background(), buildTestPDF([]string{"", "", ""}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "" || out.CharCount != 0 {
		t.Fatalf("expected no text, got %q (%d chars)", out.Text, out.CharCount)
	}
	if out.Pages != 3 {
		t.Fatalf("Pages = %d, want 3", out.Pages)
	}
	for _, want := range []string{"3 page", "scan", "ocr_image"} {
		if !strings.Contains(out.Note, want) {
			t.Fatalf("note should mention %q: %s", want, out.Note)
		}
	}
}

func TestPDFTextNoteCutsALongPageList(t *testing.T) {
	blank := make([]int, 30)
	for i := range blank {
		blank[i] = i + 1
	}
	note := pdfTextNote(40, blank)
	if !strings.Contains(note, "30 of 40 pages") || !strings.Contains(note, "20, ...") || strings.Contains(note, "21") {
		t.Fatalf("a long list should stop after 20 pages: %s", note)
	}
	if pdfTextNote(0, nil) != "" || pdfTextNote(5, nil) != "" {
		t.Fatal("a PDF with no blank pages must not carry a note")
	}
}

// gbkBytes is what Excel's "CSV (comma delimited)" and Notepad's "ANSI" write on a Chinese Windows machine.
func gbkBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", s, err)
	}
	return b
}

func TestParsePlainTextReportsEncodingOnlyWhenNotUTF8(t *testing.T) {
	plain := parsePlainText([]byte("合同"), "txt")
	if plain.Encoding != "" || plain.Text != "合同" || plain.CharCount != 2 {
		t.Fatalf("UTF-8 file: %+v", plain)
	}
	legacy := parsePlainText(gbkBytes(t, strings.Repeat("合同金额", 10)), "csv")
	if legacy.Encoding != "gb18030" || legacy.Text != strings.Repeat("合同金额", 10) || legacy.CharCount != 40 {
		t.Fatalf("GBK file: encoding=%q chars=%d text=%q", legacy.Encoding, legacy.CharCount, legacy.Text)
	}
}
