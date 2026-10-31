package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func inspect(t *testing.T, ts []tool.BaseTool, in map[string]any) (inspectPptxOutput, error) {
	t.Helper()
	raw, err := run(t, ToolInspectPptx, ts, in)
	if err != nil {
		return inspectPptxOutput{}, err
	}
	var out inspectPptxOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bad output: %v\n%s", err, raw)
	}
	return out, nil
}

func TestInspectPptxDescribesAGeneratedDeck(t *testing.T) {
	root, created, _ := buildSampleDeck(t)
	ts, err := Tools(Env{Resolve: jailTo(root)})
	if err != nil {
		t.Fatal(err)
	}
	out, err := inspect(t, ts, map[string]any{"relative_path": "decks/review.pptx"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Slides != created.Slides {
		t.Fatalf("slides = %d, created %d", out.Slides, created.Slides)
	}
	if !strings.HasPrefix(out.Size, "13.33 x 7.50") {
		t.Errorf("slide size = %q", out.Size)
	}
	// Our own decks must pass our own checks.
	for _, is := range out.Issues {
		if is.Level == "warn" {
			t.Errorf("slide %d: unexpected warning: %s", is.Slide, is.Message)
		}
	}
	if len(out.Items) != created.Slides {
		t.Fatalf("items = %d, want %d", len(out.Items), created.Slides)
	}
	first := out.Items[0]
	if first.Title != "2026 年第三季度网络运营复盘" || first.Layout != "Title Slide" {
		t.Errorf("cover = %+v", first)
	}
	if !strings.Contains(strings.Join(first.Text, "|"), "覆盖率、故障与投诉三条主线") {
		t.Errorf("cover subtitle missing: %v", first.Text)
	}

	bullets := out.Items[1]
	if bullets.Title != "三季度整体达成目标，故障量同比下降" || bullets.Layout != "Title and Content" {
		t.Errorf("bullets = %+v", bullets)
	}
	body := strings.Join(bullets.Text, "\n")
	// The ** markers are formatting, not text; "- " sub-bullets are level 1.
	for _, want := range []string{"- 覆盖率 98.6%，较二季度提升 0.4 个百分点", "\n  - 新增站点 126 个", "\n- 重大故障 2 起，均在 SLA 内恢复"} {
		if !strings.Contains(body, want) {
			t.Errorf("bullet text %q missing from %q", want, body)
		}
	}
	if !strings.Contains(bullets.Notes, "先讲结论") || !strings.Contains(bullets.Notes, "再讲原因") {
		t.Errorf("notes = %q", bullets.Notes)
	}

	var tables, charts, pictures int
	var line, column *pptxChartInfo
	for i := range out.Items {
		it := &out.Items[i]
		tables += len(it.Tables)
		pictures += it.Pictures
		for j := range it.Charts {
			charts++
			switch it.Charts[j].Type {
			case "line":
				line = &it.Charts[j]
			case "column":
				column = &it.Charts[j]
			}
		}
	}
	if tables != 3 {
		t.Errorf("tables = %d, want 3 (30 rows over 3 slides)", tables)
	}
	if charts != 6 || pictures != 1 {
		t.Errorf("charts = %d pictures = %d, want 6 and 1", charts, pictures)
	}
	if line == nil || len(line.Categories) != 4 || len(line.Series) != 1 || line.Series[0].Name != "覆盖率" ||
		strings.Join(line.Series[0].Values, ",") != "97.1,98.2,98.6,99" {
		t.Errorf("line chart = %+v", line)
	}
	if column == nil || len(column.Series) != 2 || column.Series[1].Name != "8月" {
		t.Errorf("column chart = %+v", column)
	}
	for _, it := range out.Items {
		if len(it.Tables) > 0 {
			tb := it.Tables[0]
			if tb.Cols != 4 || len(tb.Head) != 4 || tb.Head[0] != "地市" || tb.Rows != 11 {
				t.Errorf("table = %+v", tb)
			}
			break
		}
	}
}

// rewriteZip copies a zip, passing every part through edit.
func rewriteZip(t *testing.T, data []byte, edit func(name string, b []byte) []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(edit(f.Name, b)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const trickySlide = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree>
<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>
<p:sp><p:nvSpPr><p:cNvPr id="2" name="Body A"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="914400"/><a:ext cx="4572000" cy="1371600"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:p><a:r><a:rPr lang="en-US" sz="800"/><a:t>tiny text</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="3" name="Body B"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="914400" y="1371600"/><a:ext cx="4572000" cy="1371600"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:p><a:r><a:rPr lang="en-US" sz="1800"/><a:t>overlapping text</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="4" name="Body C"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="11582400" y="4572000"/><a:ext cx="3657600" cy="457200"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:p><a:r><a:rPr lang="en-US" sz="1800"/><a:t>off the edge</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="5" name="Squeezed"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="914400" y="5029200"/><a:ext cx="3657600" cy="365760"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:p><a:r><a:rPr lang="en-US" sz="2400"/><a:t>This sentence is far too long for a box that is only 0.4 inch tall and four inches wide, so it wraps over several lines</a:t></a:r></a:p></p:txBody></p:sp>
</p:spTree></p:cSld></p:sld>`

func TestInspectPptxFlagsLayoutProblems(t *testing.T) {
	root, _, data := buildSampleDeck(t)
	bad := rewriteZip(t, data, func(name string, b []byte) []byte {
		if name == "ppt/slides/slide2.xml" {
			return []byte(trickySlide)
		}
		return b
	})
	if err := os.WriteFile(filepath.Join(root, "decks", "bad.pptx"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	out, err := inspect(t, ts, map[string]any{"relative_path": "decks/bad.pptx", "slides": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].N != 2 {
		t.Fatalf("items = %+v", out.Items)
	}
	var msgs []string
	for _, is := range out.Issues {
		if is.Slide != 2 {
			t.Errorf("the other slides are untouched, but slide %d: %s", is.Slide, is.Message)
		}
		msgs = append(msgs, is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"no title", "very small text (8 pt)", `"Body A" and "Body B" overlap`, `"Body C" extends past the slide edge`, `"Squeezed" may overflow`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing issue %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, `"Body A" and "Body C"`) || strings.Contains(all, `"Squeezed" extends`) {
		t.Errorf("false positives in:\n%s", all)
	}
	if len(out.Items[0].Text) != 4 {
		t.Errorf("text = %v", out.Items[0].Text)
	}
}

func TestInspectPptxFlagsOverflowFromAnOverfullSlide(t *testing.T) {
	root, ts := newXlsxTools(t)
	long := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		long = append(long, "这是一条很长的要点，用来把这一页塞满，直到字号缩到最小也放不下为止，需要拆页处理。")
	}
	raw, err := run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "d.pptx", "title": "t",
		"slides": []map[string]any{{"layout": "bullets", "title": "塞满的一页", "bullets": long}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var created createPptxOutput
	_ = json.Unmarshal([]byte(raw), &created)
	if len(created.Warnings) == 0 {
		t.Fatalf("the generator should warn: %s", raw)
	}
	out, err := inspect(t, ts, map[string]any{"relative_path": "d.pptx"})
	if err != nil {
		t.Fatal(err)
	}
	var overflow, dense bool
	for _, is := range out.Issues {
		overflow = overflow || strings.Contains(is.Message, "may overflow")
		dense = dense || strings.Contains(is.Message, "dense slide")
	}
	if !overflow || !dense {
		t.Errorf("overflow=%v dense=%v issues=%+v", overflow, dense, out.Issues)
	}
	_ = root
}

func TestInspectPptxArguments(t *testing.T) {
	root, _, _ := buildSampleDeck(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})

	out, err := inspect(t, ts, map[string]any{"relative_path": "decks/review.pptx", "slides": "2-3, 5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 3 || out.Items[0].N != 2 || out.Items[2].N != 5 {
		t.Errorf("items = %+v", out.Items)
	}
	for _, bad := range []string{"0", "99", "3-2", "x", "1-99"} {
		if _, err := inspect(t, ts, map[string]any{"relative_path": "decks/review.pptx", "slides": bad}); err == nil {
			t.Errorf("slides %q should be rejected", bad)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "decks", "old.ppt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "decks", "junk.pptx"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"decks/old.ppt":      ".pptx",
		"decks/junk.pptx":    "cannot open",
		"decks/missing.pptx": "",
		"../escape.pptx":     "",
		"":                   "required",
	}
	for p, want := range cases {
		_, err := inspect(t, ts, map[string]any{"relative_path": p})
		if err == nil {
			t.Errorf("%q should fail", p)
		} else if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %q lacks %q", p, err, want)
		}
	}
}

// testdata/python-pptx.pptx was made by python-pptx from its stock template
// (4:3 slides, placeholders that inherit their geometry from the layout, a
// native chart and table, notes): a producer very unlike ours.
func TestInspectPptxReadsADeckFromAnotherProducer(t *testing.T) {
	root, ts := newXlsxTools(t)
	data, err := os.ReadFile(filepath.Join("testdata", "python-pptx.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.pptx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := inspect(t, ts, map[string]any{"relative_path": "other.pptx"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Slides != 4 || len(out.Items) != 4 || out.Size != "10.00 x 7.50 in" {
		t.Fatalf("deck = %d slides, %d items, size %q", out.Slides, len(out.Items), out.Size)
	}
	it := out.Items
	if it[0].Title != "python-pptx 样例" || it[0].Layout != "Title Slide" || len(it[0].Text) != 1 {
		t.Errorf("slide 1 = %+v", it[0])
	}
	if it[1].Title != "要点页" || it[1].Notes != "讲稿：先说结论" ||
		it[1].Text[0] != "第一点：覆盖率提升\n  - 子点 A\n第二点：故障下降" {
		t.Errorf("slide 2 = %+v", it[1])
	}
	if len(it[2].Tables) != 1 || it[2].Tables[0].Rows != 3 || it[2].Tables[0].Data[1][2] != "R2C2" {
		t.Errorf("slide 3 table = %+v", it[2].Tables)
	}
	if len(it[2].Charts) != 1 || it[2].Charts[0].Type != "column" || strings.Join(it[2].Charts[0].Series[0].Values, ",") != "1.5,2.5,3.5" {
		t.Errorf("slide 3 chart = %+v", it[2].Charts)
	}
	// Only the real problems are reported: slide 4 has no title; the chart lacks alt text.
	var warns []string
	for _, is := range out.Issues {
		if is.Level == "warn" {
			warns = append(warns, is.Message)
		}
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "no title") {
		t.Errorf("warnings = %v", warns)
	}
}

// "issues" is always an array: [] means "checked, nothing found", null would read as "not checked".
func TestInspectPptxReportsAnEmptyIssueList(t *testing.T) {
	root := t.TempDir()
	ts, err := Tools(Env{Resolve: jailTo(root)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "decks/tiny.pptx", "title": "一页就够",
		"slides": []map[string]any{{"layout": "bullets", "title": "结论先行", "bullets": []string{"覆盖率 98.6%", "故障均在 SLA 内恢复"}}},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := run(t, ToolInspectPptx, ts, map[string]any{"relative_path": "decks/tiny.pptx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"issues":[]`) {
		t.Fatalf("a clean deck must report an empty issue array, got: %s", raw)
	}
}

func TestInspectPptxIsReadOnly(t *testing.T) {
	root, _, data := buildSampleDeck(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	if _, err := inspect(t, ts, map[string]any{"relative_path": "decks/review.pptx"}); err != nil {
		t.Fatal(err)
	}
	if after := mustRead(t, filepath.Join(root, "decks", "review.pptx")); !bytes.Equal(after, data) {
		t.Error("inspect_pptx changed the file")
	}
	if !ReadOnly(ToolInspectPptx) || !ReadOnly(ToolReadXlsx) || ReadOnly(ToolCreatePptx) || ReadOnly(ToolEditXlsx) {
		t.Error("ReadOnly classification is wrong")
	}
}
