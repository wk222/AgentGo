package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

// sampleDeck exercises every layout, the notes and the closing slide.
func sampleDeck() map[string]any {
	rows := make([][]any, 0, 30)
	for i := 1; i <= 30; i++ {
		rows = append(rows, []any{fmt.Sprintf("地市%02d", i), 1000 + i*37, fmt.Sprintf("%.1f%%", 90+float64(i)/10), "按期完成"})
	}
	return map[string]any{
		"relative_path": "decks/review.pptx",
		"title":         "2026 年第三季度网络运营复盘",
		"subtitle":      "覆盖率、故障与投诉三条主线",
		"author":        "网络部",
		"date":          "2026年10月",
		"theme":         "mobile",
		"closing":       "谢谢，欢迎提问",
		"slides": []map[string]any{
			{"layout": "bullets", "title": "三季度整体达成目标，故障量同比下降", "notes": "先讲结论。\n再讲原因。",
				"bullets": []string{"覆盖率 **98.6%**，较二季度提升 0.4 个百分点", "- 新增站点 126 个", "- 弱覆盖区域整改 38 处", "重大故障 **2 起**，均在 SLA 内恢复", "投诉量环比下降 12%"}},
			{"layout": "two_column", "title": "做得好的与要改进的",
				"left":  map[string]any{"heading": "亮点", "bullets": []string{"自动化巡检覆盖 80% 站点", "故障定位时间缩短一半"}},
				"right": map[string]any{"heading": "不足", "text": "夜间值班人手紧张，偏远地区备件周转慢。"}},
			{"layout": "stats", "title": "关键指标一览",
				"stats": []map[string]any{{"value": "98.6%", "label": "网络覆盖率", "note": "较上季 +0.4pt"}, {"value": "2", "label": "重大故障"}, {"value": "1.2万", "label": "投诉量", "note": "环比 -12%"}}},
			{"layout": "table", "title": "各地市工单完成情况", "caption": "数据来源：工单系统，截至 9 月 30 日",
				"columns": []string{"地市", "工单量", "完成率", "状态"}, "rows": rows},
			{"layout": "chart", "title": "覆盖率逐季提升", "takeaway": []string{"二季度起增速加快", "年底有望突破 99%"},
				"chart": map[string]any{"type": "line", "categories": []string{"一季度", "二季度", "三季度", "四季度"},
					"series": []map[string]any{{"name": "覆盖率", "values": []float64{97.1, 98.2, 98.6, 99.0}}}, "unit": "%"}},
			{"layout": "chart", "title": "各片区故障量对比",
				"chart": map[string]any{"type": "column", "categories": []string{"济南", "青岛", "烟台", "潍坊"},
					"series": []map[string]any{{"name": "7月", "values": []float64{12, 9, 7, 5}}, {"name": "8月", "values": []float64{10, 8, 8, 4}}}}},
			{"layout": "chart", "title": "投诉类型占比",
				"chart": map[string]any{"type": "pie", "categories": []string{"信号", "资费", "服务", "其他"},
					"series": []map[string]any{{"name": "占比", "values": []float64{45, 25, 20, 10}}}}},
			{"layout": "chart", "title": "堆叠与横向图表", "chart": map[string]any{"type": "stacked_column", "categories": []string{"A", "B", "C"},
				"series": []map[string]any{{"name": "x", "values": []float64{1, 2, 3}}, {"name": "y", "values": []float64{3, 2, 1}}}}},
			{"layout": "chart", "title": "横向条形", "chart": map[string]any{"type": "bar", "categories": []string{"甲", "乙", "丙"},
				"series": []map[string]any{{"name": "z", "values": []float64{5, 3, 8}}}}},
			{"layout": "chart", "title": "环形图", "chart": map[string]any{"type": "doughnut", "categories": []string{"甲", "乙"},
				"series": []map[string]any{{"name": "z", "values": []float64{60, 40}}}}},
			{"layout": "section", "title": "第二部分：下季度计划", "body": "三件事，各有负责人"},
			{"layout": "timeline", "title": "十月到十二月的节奏",
				"steps": []map[string]any{{"title": "10 月", "detail": "完成弱覆盖整改"}, {"title": "11 月", "detail": "上线智能巡检二期"}, {"title": "12 月", "detail": "年度复盘与评优"}}},
			{"layout": "image", "title": "站点分布示意", "image": "assets/map.png", "caption": "图：全省站点分布（示意）"},
		},
	}
}

func writeTestPNG(t *testing.T, root string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x / 3), uint8(y / 2), 180, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "map.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildSampleDeck(t *testing.T) (root string, out createPptxOutput, data []byte) {
	t.Helper()
	root, ts := newXlsxTools(t)
	writeTestPNG(t, root)
	raw, err := run(t, ToolCreatePptx, ts, sampleDeck())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return root, out, mustRead(t, filepath.Join(root, "decks", "review.pptx"))
}

// checkPackage verifies what PowerPoint insists on: well-formed parts, a content
// type for every part, relationship targets that exist, unique shape ids.
func checkPackage(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = string(b)
	}
	if zr.File[0].Name != "[Content_Types].xml" {
		t.Errorf("[Content_Types].xml must be first, got %s", zr.File[0].Name)
	}

	var ct struct {
		Default []struct {
			Extension string `xml:"Extension,attr"`
		} `xml:"Default"`
		Override []struct {
			PartName string `xml:"PartName,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal([]byte(parts["[Content_Types].xml"]), &ct); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]bool{}
	for _, d := range ct.Default {
		defaults[d.Extension] = true
	}
	overrides := map[string]bool{}
	for _, o := range ct.Override {
		overrides[strings.TrimPrefix(o.PartName, "/")] = true
		if _, ok := parts[strings.TrimPrefix(o.PartName, "/")]; !ok {
			t.Errorf("content type override for missing part %s", o.PartName)
		}
	}
	idRe := regexp.MustCompile(`<p:cNvPr id="(\d+)"`)
	for name, body := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			wellFormed(t, name, body)
		}
		if name != "[Content_Types].xml" && !overrides[name] && !defaults[strings.TrimPrefix(path.Ext(name), ".")] {
			t.Errorf("part %s has no content type", name)
		}
		if strings.HasSuffix(name, ".rels") {
			base := path.Dir(path.Dir(name)) // directory of the part the rels belong to
			var rels struct {
				Rel []struct {
					Target string `xml:"Target,attr"`
					Mode   string `xml:"TargetMode,attr"`
				} `xml:"Relationship"`
			}
			if err := xml.Unmarshal([]byte(body), &rels); err != nil {
				t.Fatal(err)
			}
			for _, r := range rels.Rel {
				target := path.Join(base, r.Target)
				if _, ok := parts[target]; !ok {
					t.Errorf("%s points at missing %s", name, target)
				}
			}
		}
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			seen := map[string]bool{}
			for _, m := range idRe.FindAllStringSubmatch(body, -1) {
				if seen[m[1]] {
					t.Errorf("%s repeats shape id %s", name, m[1])
				}
				seen[m[1]] = true
			}
		}
	}
	return parts
}

func TestCreatePptxBuildsAValidPackage(t *testing.T) {
	_, out, data := buildSampleDeck(t)
	parts := checkPackage(t, data)

	// 1 cover + 11 content slides (the table splits in two: +1) + closing
	if out.Slides < 14 || len(out.Outline) != out.Slides {
		t.Fatalf("slides = %d outline = %v", out.Slides, out.Outline)
	}
	if !strings.Contains(out.Outline[0], "2026 年第三季度网络运营复盘") || !strings.Contains(out.Outline[len(out.Outline)-1], "谢谢") {
		t.Errorf("outline ends = %q ... %q", out.Outline[0], out.Outline[len(out.Outline)-1])
	}
	for _, n := range []int{1, 2} {
		if _, ok := parts[fmt.Sprintf("ppt/slides/slide%d.xml", n)]; !ok {
			t.Errorf("slide%d missing", n)
		}
	}
	// The title is a real title placeholder with the text on it.
	if s2 := parts["ppt/slides/slide2.xml"]; !strings.Contains(s2, `<p:ph type="title"/>`) || !strings.Contains(s2, "三季度整体达成目标") {
		t.Errorf("slide 2 has no title placeholder: %.300s", s2)
	}
	// Notes exist only where asked.
	if _, ok := parts["ppt/notesSlides/notesSlide2.xml"]; !ok || !strings.Contains(parts["ppt/notesSlides/notesSlide2.xml"], "再讲原因") {
		t.Error("notes of slide 2 missing")
	}
	if _, ok := parts["ppt/notesSlides/notesSlide3.xml"]; ok {
		t.Error("slide 3 has no notes and must not get a notes part")
	}
	// Six chart slides, each with a workbook excelize can open.
	var charts int
	for name, body := range parts {
		if strings.HasPrefix(name, "ppt/embeddings/") {
			charts++
			f, err := excelize.OpenReader(strings.NewReader(body))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if v, _ := f.GetCellValue("Sheet1", "A2"); v == "" {
				t.Errorf("%s has no categories", name)
			}
			f.Close()
		}
	}
	if charts != 6 {
		t.Errorf("charts = %d, want 6", charts)
	}
	if !strings.Contains(parts["ppt/media/image1.png"], "PNG") {
		t.Error("picture not stored")
	}
	if !strings.Contains(parts["docProps/core.xml"], "<dc:creator>网络部</dc:creator>") {
		t.Errorf("core props: %s", parts["docProps/core.xml"])
	}
}

func TestCreatePptxSplitsLongTables(t *testing.T) {
	_, out, _ := buildSampleDeck(t)
	var parts []string
	for _, o := range out.Outline {
		if strings.Contains(o, "各地市工单完成情况") {
			parts = append(parts, o)
		}
	}
	if len(parts) < 2 || !strings.Contains(parts[0], "（1/") || !strings.Contains(parts[1], "（2/") {
		t.Errorf("table slides = %v", parts)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "split over") {
			found = true
		}
	}
	if !found {
		t.Errorf("the split must be reported: %v", out.Warnings)
	}
}

func TestCreatePptxRejectsBadInput(t *testing.T) {
	root, ts := newXlsxTools(t)
	base := func(slide map[string]any) map[string]any {
		return map[string]any{"relative_path": "x.pptx", "title": "T", "slides": []map[string]any{slide}}
	}
	cases := map[string]map[string]any{
		"no slides":      {"relative_path": "x.pptx", "title": "T", "slides": []map[string]any{}},
		"no title":       {"relative_path": "x.pptx", "title": " ", "slides": []map[string]any{{"layout": "bullets", "title": "a", "bullets": []string{"b"}}}},
		"bad theme":      {"relative_path": "x.pptx", "title": "T", "theme": "neon", "slides": []map[string]any{{"layout": "bullets", "title": "a", "bullets": []string{"b"}}}},
		"bad layout":     base(map[string]any{"layout": "hero", "title": "a"}),
		"slide no title": base(map[string]any{"layout": "bullets", "bullets": []string{"b"}}),
		"no bullets":     base(map[string]any{"layout": "bullets", "title": "a"}),
		"one column":     base(map[string]any{"layout": "two_column", "title": "a", "left": map[string]any{"bullets": []string{"x"}}}),
		"ragged table":   base(map[string]any{"layout": "table", "title": "a", "columns": []string{"a", "b"}, "rows": [][]any{{"1"}}}),
		"one stat":       base(map[string]any{"layout": "stats", "title": "a", "stats": []map[string]any{{"value": "1", "label": "x"}}}),
		"one step":       base(map[string]any{"layout": "timeline", "title": "a", "steps": []map[string]any{{"title": "x"}}}),
		"no chart":       base(map[string]any{"layout": "chart", "title": "a"}),
		"short series": base(map[string]any{"layout": "chart", "title": "a", "chart": map[string]any{"type": "column",
			"categories": []string{"a", "b"}, "series": []map[string]any{{"name": "s", "values": []float64{1}}}}}),
		"bad chart type": base(map[string]any{"layout": "chart", "title": "a", "chart": map[string]any{"type": "radar",
			"categories": []string{"a"}, "series": []map[string]any{{"name": "s", "values": []float64{1}}}}}),
		"missing image":  base(map[string]any{"layout": "image", "title": "a", "image": "nope.png"}),
		"gif image":      base(map[string]any{"layout": "image", "title": "a", "image": "a.gif"}),
		"escaping image": base(map[string]any{"layout": "image", "title": "a", "image": "../x.png"}),
	}
	for name, args := range cases {
		if _, err := run(t, ToolCreatePptx, ts, args); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("failed calls left files behind: %v", entries)
	}
	// An error names the slide.
	_, err := run(t, ToolCreatePptx, ts, base(map[string]any{"layout": "table", "title": "a", "columns": []string{"a", "b"}, "rows": [][]any{{"1"}}}))
	if err == nil || !strings.Contains(err.Error(), "slides[0] (table)") || !strings.Contains(err.Error(), "rows[0]") {
		t.Errorf("error = %v", err)
	}
}

func TestCreatePptxProtectsExistingFiles(t *testing.T) {
	root, ts := newXlsxTools(t)
	args := map[string]any{"relative_path": "a", "title": "T", "slides": []map[string]any{{"layout": "bullets", "title": "a", "bullets": []string{"b"}}}}
	if _, err := run(t, ToolCreatePptx, ts, args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.pptx")); err != nil {
		t.Fatalf("extension not added: %v", err)
	}
	if _, err := run(t, ToolCreatePptx, ts, args); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write: %v", err)
	}
	args["overwrite"] = true
	if _, err := run(t, ToolCreatePptx, ts, args); err != nil {
		t.Errorf("overwrite: %v", err)
	}
}

func TestSlidesWarnWhenTextCannotFit(t *testing.T) {
	_, ts := newXlsxTools(t)
	long := strings.Repeat("这是一条很长很长的要点，需要占用很多行才能放得下。", 4)
	items := make([]string, 12)
	for i := range items {
		items[i] = long
	}
	raw, err := run(t, ToolCreatePptx, ts, map[string]any{"relative_path": "w.pptx", "title": "T",
		"slides": []map[string]any{{"layout": "bullets", "title": "要点", "bullets": items}}})
	if err != nil {
		t.Fatal(err)
	}
	var out createPptxOutput
	_ = json.Unmarshal([]byte(raw), &out)
	if len(out.Warnings) == 0 || !strings.Contains(out.Warnings[0], "slide 2") {
		t.Errorf("an overfull slide must warn: %v", out.Warnings)
	}
}

func TestTextEstimates(t *testing.T) {
	// 20 CJK characters at 1 em each fill exactly one 20 em line.
	if got := countLines(strings.Repeat("字", 20), 20); got != 1 {
		t.Errorf("20 CJK in 20 em = %d lines", got)
	}
	if got := countLines(strings.Repeat("字", 21), 20); got != 2 {
		t.Errorf("21 CJK in 20 em = %d lines", got)
	}
	// English words wrap whole.
	if got := countLines("hello world", 4); got != 2 {
		t.Errorf("two words in 4 em = %d lines", got)
	}
	if got := countLines("hello world", 6); got != 1 {
		t.Errorf("two short words in 6 em = %d lines", got)
	}
	if got := countLines("a\nb\nc", 50); got != 3 {
		t.Errorf("hard breaks = %d", got)
	}
	if got := countLines("", 10); got != 1 {
		t.Errorf("empty = %d", got)
	}
	sp := spans("a **b** c", false)
	if len(sp) != 3 || !sp[1].bold || sp[0].bold || sp[2].bold {
		t.Errorf("spans = %+v", sp)
	}
	if sp := spans("a ** b", false); len(sp) != 1 || sp[0].text != "a ** b" {
		t.Errorf("unbalanced markers stay literal: %+v", sp)
	}
}

var _ = []tool.BaseTool(nil)
