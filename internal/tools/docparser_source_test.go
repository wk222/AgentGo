package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

func TestParseFileRegisteredToolUsesBoundedRevisionAwareOutput(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("合同🙂", 3000)
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	if err := registerParseFile(r, root); err != nil {
		t.Fatal(err)
	}
	tool, ok := r.Get("parse_file")
	if !ok {
		t.Fatal("parse_file not registered")
	}
	inv := tool.(einotool.InvokableTool)
	raw, err := inv.InvokableRun(context.Background(), `{"relative_path":"long.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out parseFileOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.CharCount != parsedFileDefaultLimit || out.TotalChars != 9000 || out.NextOffset == nil || out.Source == nil || len(out.Spans) != 1 {
		t.Fatalf("registered tool did not return bounded source metadata: %+v", out)
	}
	args, err := json.Marshal(parseFileInput{RelativePath: "long.txt", Offset: *out.NextOffset, ExpectedRevision: out.Source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = inv.InvokableRun(context.Background(), string(args))
	if err != nil {
		t.Fatal(err)
	}
	var tail parseFileOutput
	if err := json.Unmarshal([]byte(raw), &tail); err != nil {
		t.Fatal(err)
	}
	if out.Text+tail.Text != text || tail.NextOffset != nil {
		t.Fatal("registered tool continuation lost text")
	}
}

func TestParsedPDFSourceRangesSurviveBlankPagesAndRepeatedText(t *testing.T) {
	out, err := parsePDF(context.Background(), buildTestPDF([]string{"Repeated contract", "", "Repeated contract"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Spans) != 2 || out.Spans[0].Page != 1 || out.Spans[1].Page != 3 {
		t.Fatalf("page identity lost: %+v", out.Spans)
	}
	text := []rune(out.Text)
	for _, span := range out.Spans {
		if got := string(text[span.Start:span.End]); got != "Repeated contract" {
			t.Fatalf("page %d range points at %q", span.Page, got)
		}
	}
	if out.Spans[0].Start == out.Spans[1].Start {
		t.Fatal("repeated content must have distinct positions")
	}
}

func TestParsedXLSXIncludesHeadersAllSheetsAndOriginalSparseRows(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "预算"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("执行"); err != nil {
		t.Fatal(err)
	}
	for _, cell := range []struct{ sheet, axis, value string }{
		{"预算", "A1", "项目"}, {"预算", "B1", "金额"},
		{"预算", "A4", "机房"}, {"预算", "B4", "一百"},
		{"执行", "A1", "负责人"}, {"执行", "A7", "李四🙂"},
	} {
		if err := f.SetCellStr(cell.sheet, cell.axis, cell.value); err != nil {
			t.Fatal(err)
		}
	}
	data, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseXLSX(context.Background(), data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if out.Sheets != 2 || out.Pages != 0 || len(out.Spans) != 4 {
		t.Fatalf("wrong workbook coverage: %+v", out)
	}
	text := []rune(out.Text)
	want := []struct {
		sheet string
		row   int
		text  string
	}{
		{"预算", 1, "项目\t金额"}, {"预算", 4, "机房\t一百"},
		{"执行", 1, "负责人"}, {"执行", 7, "李四🙂"},
	}
	for i, w := range want {
		s := out.Spans[i]
		if s.Sheet != w.sheet || s.RowStart != w.row || s.RowEnd != w.row || string(text[s.Start:s.End]) != w.text {
			t.Fatalf("span %d: %+v text=%q; want %+v", i, s, string(text[s.Start:s.End]), w)
		}
	}
}

func TestParsedFileWindowRoundTripAndRevisionGuard(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "报告.txt")
	text := "第一行🙂\n第二行合同金额"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	var hash string
	var revision string
	offset := 0
	for {
		out, err := readParsedFile(context.Background(), root, parseFileInput{RelativePath: "报告.txt", Offset: offset, Limit: 3, ExpectedSHA256: hash, ExpectedRevision: revision})
		if err != nil {
			t.Fatal(err)
		}
		if out.Source == nil || out.Source.ParserVersion != parsedFileVersion || out.TotalChars != len([]rune(text)) {
			t.Fatalf("missing revision: %+v", out)
		}
		if out.Offset != offset || out.CharCount > 3 || len(out.Spans) != 1 {
			t.Fatalf("bad window: %+v", out)
		}
		hash = out.Source.SHA256
		revision = out.Source.Revision
		combined.WriteString(out.Text)
		if out.NextOffset == nil {
			break
		}
		if *out.NextOffset <= offset {
			t.Fatal("continuation made no progress")
		}
		offset = *out.NextOffset
	}
	if combined.String() != text {
		t.Fatalf("windowed reads changed Unicode text: %q", combined.String())
	}
	if err := os.WriteFile(path, []byte("修改后的合同"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := readParsedFile(context.Background(), root, parseFileInput{RelativePath: "报告.txt", Offset: 3, ExpectedSHA256: hash})
	if err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("stale continuation accepted: %v", err)
	}
	newOut, err := readParsedFile(context.Background(), root, parseFileInput{RelativePath: "报告.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if newOut.Source.SHA256 == hash || newOut.Text != "修改后的合同" {
		t.Fatal("revision was not refreshed")
	}
	_, err = readParsedFile(context.Background(), root, parseFileInput{RelativePath: "报告.txt", ExpectedRevision: revision})
	if err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("stale parsed revision accepted: %v", err)
	}
}

func TestParsedFileRejectsBadWindowsAndWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("中文"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, in := range []parseFileInput{
		{RelativePath: "a.txt", Offset: -1}, {RelativePath: "a.txt", Limit: -1},
		{RelativePath: "a.txt", Offset: 3}, {RelativePath: "../outside.txt"},
		{RelativePath: "a.txt", Limit: parsedFileMaxLimit + 1},
	} {
		if _, err := readParsedFile(context.Background(), root, in); err == nil {
			t.Fatalf("accepted invalid input %+v", in)
		}
	}
	out, err := readParsedFile(context.Background(), root, parseFileInput{RelativePath: "a.txt", Offset: 2})
	if err != nil || out.Text != "" || out.NextOffset != nil || len(out.Spans) != 0 {
		t.Fatalf("bad EOF window: %+v %v", out, err)
	}
}
