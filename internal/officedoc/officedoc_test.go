package officedoc

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	docxparser "github.com/cloudwego/eino-ext/components/document/parser/docx"
	xlsxparser "github.com/cloudwego/eino-ext/components/document/parser/xlsx"
	"github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

const sampleMarkdown = "# 周报 Weekly Report\n\n" +
	"本周完成 **三项** 工作,*两项*延期,~~一项~~取消,见 `make test`。详见 [文档](https://example.com/a)。\n\n" +
	"## 待办\n\n" +
	"- 修复登录\n  - 子项 A\n  - 子项 B\n- 发布 v1.2\n\n" +
	"1. 第一步\n2. 第二步\n\n" +
	"Between.\n\n" +
	"1. 重新开始\n\n" +
	"| 任务 | 负责人 | 状态 |\n|---|---|---|\n| 登录 | 张三 | 完成 |\n| 发布 | 李四 | 进行中 |\n\n" +
	"> 引用: 先做完再做好\n\n" +
	"```go\nfmt.Println(\"a < b && c\")\n```\n\n" +
	"---\n\n" +
	"<!-- pagebreak -->\n\n" +
	"最后一页\x00\x1b 结束\n"

func buildDocx(t *testing.T, md string) ([]byte, *Doc) {
	t.Helper()
	d := &Doc{}
	AddMarkdown(d, md)
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), d
}

func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func wellFormed(t *testing.T, name, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v", name, err)
		}
	}
}

func TestDocxPackageIsWellFormedAndComplete(t *testing.T) {
	data, d := buildDocx(t, sampleMarkdown)
	parts := readZip(t, data)
	for _, name := range []string{
		"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "word/_rels/document.xml.rels",
		"word/styles.xml", "word/numbering.xml", "word/document.xml",
	} {
		body, ok := parts[name]
		if !ok {
			t.Fatalf("missing part %s", name)
		}
		wellFormed(t, name, body)
	}
	doc := parts["word/document.xml"]
	for _, want := range []string{
		`w:val="Heading1"`, `w:val="Heading2"`, `<w:tbl>`, `<w:tblHeader/>`, `w:val="Quote"`, `w:val="Code"`,
		`w:type="page"`, `<w:ilvl w:val="1"/>`, "a &lt; b &amp;&amp; c",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml lacks %s", want)
		}
	}
	if strings.ContainsAny(doc, "\x00\x1b") {
		t.Error("control characters leaked into document.xml")
	}
	// Word wants a paragraph after a table.
	if !strings.Contains(doc, `</w:tbl><w:p/>`) {
		t.Error("no paragraph after the table")
	}
	// Two numbered lists, each restarting at 1: numIds 2 and 3, plus the bullet list 1.
	num := parts["word/numbering.xml"]
	for _, id := range []string{`w:numId="1"`, `w:numId="2"`, `w:numId="3"`} {
		if !strings.Contains(num, id) {
			t.Errorf("numbering.xml lacks %s", id)
		}
	}
	if strings.Contains(num, `w:numId="4"`) {
		t.Error("more numbered lists than the document has")
	}
	if !strings.Contains(parts["docProps/core.xml"], "周报 Weekly Report") && d.Title == "" {
		// the title comes from the tool layer; the core part must at least be valid
		wellFormed(t, "core", parts["docProps/core.xml"])
	}
	// Every style the document refers to is defined.
	styles := parts["word/styles.xml"]
	for _, id := range []string{"Heading1", "Heading2", "ListParagraph", "Quote", "Code", "TableGrid"} {
		if !strings.Contains(styles, `w:styleId="`+id+`"`) {
			t.Errorf("styles.xml lacks %s", id)
		}
	}
}

// The repo's own DOCX reader (parse_file) must read back what was written.
func TestDocxRoundTripsThroughTheParser(t *testing.T) {
	data, _ := buildDocx(t, sampleMarkdown)
	p, err := docxparser.NewDocxParser(context.Background(), &docxparser.Config{IncludeTables: true})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := p.Parse(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var all strings.Builder
	for _, d := range docs {
		all.WriteString(d.Content)
		all.WriteString("\n")
	}
	got := all.String()
	for _, want := range []string{"周报 Weekly Report", "三项", "子项 A", "发布 v1.2", "第二步", "张三", "进行中", "先做完再做好", "最后一页"} {
		if !strings.Contains(got, want) {
			t.Errorf("round trip lost %q\n%s", want, got)
		}
	}
}

func TestEmptyDocumentIsStillValid(t *testing.T) {
	var buf bytes.Buffer
	if _, err := (&Doc{}).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	for name, body := range readZip(t, buf.Bytes()) {
		wellFormed(t, name, body)
	}
}

func TestEscapeXMLDropsWhatXMLCannotCarry(t *testing.T) {
	cases := map[string]string{
		"a<b>&\"c\"":  "a&lt;b&gt;&amp;&quot;c&quot;",
		"x\x00y\x1bz": "xyz",
		"tab\there":   "tab here",
		"\xff\xfeok":  "ok",
		"中文 😀":        "中文 😀",
	}
	for in, want := range cases {
		if got := escapeXML(in); got != want {
			t.Errorf("escapeXML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXlsxWritesTypedCellsFormulasAndHeader(t *testing.T) {
	var buf bytes.Buffer
	err := WriteXlsx(&buf, []Sheet{
		{Name: "预算", Header: true, Rows: [][]any{
			{"项目", "金额", "已付"},
			{"设备", 1200.5, true},
			{"差旅", 800, false},
			{"合计", "=SUM(B2:B3)", nil},
		}},
		{Name: "预算", Rows: [][]any{{"dup name"}}},                // duplicate name
		{Name: "bad:name/with*chars?[x]", Rows: [][]any{{"x"}}},  // illegal characters
		{Name: strings.Repeat("长", 40), Rows: [][]any{{"long"}}}, // over 31 characters
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names := f.GetSheetList()
	if len(names) != 4 || names[0] != "预算" || names[1] != "预算_2" || names[2] != "badnamewithcharsx" {
		t.Fatalf("sheet names = %v", names)
	}
	if n := len([]rune(names[3])); n > 31 {
		t.Fatalf("sheet name has %d runes", n)
	}
	if v, _ := f.GetCellValue("预算", "B2"); v != "1200.5" {
		t.Errorf("B2 = %q", v)
	}
	if ct, _ := f.GetCellType("预算", "B2"); ct == excelize.CellTypeSharedString || ct == excelize.CellTypeInlineString {
		t.Errorf("B2 type = %v, a number must not be stored as text", ct)
	}
	if ct, _ := f.GetCellType("预算", "A2"); ct != excelize.CellTypeSharedString && ct != excelize.CellTypeInlineString {
		t.Errorf("A2 type = %v, want text", ct)
	}
	if fm, _ := f.GetCellFormula("预算", "B4"); fm != "SUM(B2:B3)" { // no leading "=": that is how Excel stores it
		t.Errorf("B4 formula = %q", fm)
	}
	if ct, _ := f.GetCellType("预算", "C2"); ct != excelize.CellTypeBool {
		t.Errorf("C2 type = %v, want bool", ct)
	}
	if st, _ := f.GetCellStyle("预算", "A1"); st == 0 {
		t.Error("header row has no style")
	}
	if st, _ := f.GetCellStyle("预算", "A2"); st != 0 {
		t.Error("body row must not use the header style")
	}
	if p, _ := f.GetPanes("预算"); !p.Freeze || p.TopLeftCell != "A2" {
		t.Errorf("panes = %+v", p)
	}
	if w, _ := f.GetColWidth("预算", "A"); w < 8 {
		t.Errorf("column A width = %v", w)
	}
}

func TestXlsxRoundTripsThroughTheParser(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteXlsx(&buf, []Sheet{{Name: "S", Header: true, Rows: [][]any{{"名称", "数量"}, {"苹果", 3}}}}); err != nil {
		t.Fatal(err)
	}
	p, err := xlsxparser.NewXlsxParser(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := p.Parse(context.Background(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, d := range docs {
		all.WriteString(d.Content)
	}
	if !strings.Contains(all.String(), "苹果") || !strings.Contains(all.String(), "3") {
		t.Fatalf("parsed = %q", all.String())
	}
}

func TestXlsxRejectsAbusiveInput(t *testing.T) {
	if err := WriteXlsx(io.Discard, nil); err == nil {
		t.Error("a workbook without sheets must fail")
	}
	wide := make([]any, MaxSheetCols+1)
	if err := WriteXlsx(io.Discard, []Sheet{{Name: "x", Rows: [][]any{wide}}}); err == nil {
		t.Error("too many columns must fail")
	}
	many := make([]Sheet, MaxSheets+1)
	for i := range many {
		many[i] = Sheet{Rows: [][]any{{1}}}
	}
	if err := WriteXlsx(io.Discard, many); err == nil {
		t.Error("too many sheets must fail")
	}
}

func jailTo(root string) func(string) (string, error) {
	return func(rel string) (string, error) {
		rel = filepath.ToSlash(rel)
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
			return "", errors.New("path escapes workspace")
		}
		return filepath.Join(root, filepath.FromSlash(rel)), nil
	}
}

func run(t *testing.T, name string, tools []tool.BaseTool, args any) (string, error) {
	t.Helper()
	b, _ := json.Marshal(args)
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		if info.Name == name {
			return tl.(tool.InvokableTool).InvokableRun(context.Background(), string(b))
		}
	}
	t.Fatalf("no tool %s", name)
	return "", nil
}

func TestToolsCreateFilesSafely(t *testing.T) {
	root := t.TempDir()
	tools, err := Tools(Env{Resolve: jailTo(root)})
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(t, ToolCreateDocx, tools, map[string]any{"relative_path": "reports/w1", "markdown": "# 标题\n\n正文"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"path":"reports/w1.docx"`) {
		t.Errorf("output = %s", out)
	}
	docPath := filepath.Join(root, "reports", "w1.docx")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if core := readZip(t, data)["docProps/core.xml"]; !strings.Contains(core, "<dc:title>标题</dc:title>") {
		t.Errorf("title not taken from the first heading: %s", core)
	}

	// Existing file: refuse unless overwrite.
	if _, err := run(t, ToolCreateDocx, tools, map[string]any{"relative_path": "reports/w1.docx", "markdown": "# 新"}); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite protection: err = %v", err)
	}
	if after, _ := os.ReadFile(docPath); !bytes.Equal(after, data) {
		t.Error("a refused write changed the file")
	}
	if _, err := run(t, ToolCreateDocx, tools, map[string]any{"relative_path": "reports/w1.docx", "markdown": "# 新", "overwrite": true}); err != nil {
		t.Errorf("overwrite: %v", err)
	}

	// The jail is honoured, and nothing is written.
	if _, err := run(t, ToolCreateDocx, tools, map[string]any{"relative_path": "../evil.docx", "markdown": "x"}); err == nil {
		t.Error("path escape must fail")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.docx")); err == nil {
		t.Error("escaped file was written")
	}

	if _, err := run(t, ToolCreateDocx, tools, map[string]any{"relative_path": "e.docx", "markdown": "  "}); err == nil {
		t.Error("empty markdown must fail")
	}

	// xlsx, with the header flag defaulting to on.
	out, err = run(t, ToolCreateXlsx, tools, map[string]any{
		"relative_path": "data/b.xlsx",
		"sheets":        []map[string]any{{"name": "S", "rows": [][]any{{"a", "b"}, {1, 2}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 sheets, 2 rows") {
		t.Errorf("output = %s", out)
	}
	f, err := excelize.OpenFile(filepath.Join(root, "data", "b.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if st, _ := f.GetCellStyle("S", "A1"); st == 0 {
		t.Error("header must default to on")
	}

	// No temp files are left behind.
	entries, _ := os.ReadDir(filepath.Join(root, "reports"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".officedoc-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestToolsNeedAResolver(t *testing.T) {
	if _, err := Tools(Env{}); err == nil {
		t.Error("Tools without Resolve must fail")
	}
}
