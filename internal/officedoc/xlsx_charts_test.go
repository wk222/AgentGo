package officedoc

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

func readCharts(t *testing.T, ts []tool.BaseTool, rel, sheet string) []chartInfo {
	t.Helper()
	args := map[string]any{"relative_path": rel}
	if sheet != "" {
		args["sheet"] = sheet
	}
	raw, err := run(t, ToolReadXlsx, ts, args)
	if err != nil {
		t.Fatal(err)
	}
	var out readXlsxOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bad output: %v\n%s", err, raw)
	}
	return out.Charts
}

func editOps(ts []tool.BaseTool, t *testing.T, rel string, ops ...map[string]any) error {
	t.Helper()
	_, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": rel, "no_backup": true, "operations": ops})
	return err
}

// A workbook written by another program: the charts must be found per sheet, with
// the type, title and data ranges a model needs to judge them.
func TestReadXlsxListsChartsFromAnotherProducer(t *testing.T) {
	root, ts := newXlsxTools(t)
	data, err := os.ReadFile(filepath.Join("testdata", "openpyxl-chart.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ext.xlsx"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got := readCharts(t, ts, "ext.xlsx", "预算")
	if len(got) != 1 {
		t.Fatalf("charts on 预算 = %+v", got)
	}
	c := got[0]
	if c.Index != 1 || c.Anchor != "E2" || c.Type != "column" || c.Title != "预算构成" {
		t.Errorf("chart = %+v", c)
	}
	if len(c.Series) != 1 || c.Series[0].Values != "预算!$B$2:$B$3" || c.Series[0].Categories != "预算!$A$2:$A$3" || c.Series[0].Name != "预算!B1" {
		t.Errorf("series = %+v", c.Series)
	}

	trend := readCharts(t, ts, "ext.xlsx", "趋势")
	if len(trend) != 1 || trend[0].Anchor != "D4" || trend[0].Type != "line" || trend[0].Title != "覆盖率" {
		t.Errorf("charts on 趋势 = %+v", trend)
	}
	if none := readCharts(t, ts, "ext.xlsx", "无图"); len(none) != 0 {
		t.Errorf("a sheet without charts reported %+v", none)
	}
}

func TestEditXlsxAddsAndDeletesCharts(t *testing.T) {
	_, ts := newXlsxTools(t)
	seedBook(t, ts, "b.xlsx")
	add := func(cell, title, rng string) map[string]any {
		return map[string]any{"op": "add_chart", "sheet": "预算", "cell": cell,
			"chart": map[string]any{"type": "pie", "data_range": rng, "title": title}}
	}

	if err := editOps(ts, t, "b.xlsx", add("E2", "构成", "A1:B3")); err != nil {
		t.Fatal(err)
	}
	got := readCharts(t, ts, "b.xlsx", "预算")
	if len(got) != 1 || got[0].Anchor != "E2" || got[0].Type != "pie" || got[0].Title != "构成" {
		t.Fatalf("after add: %+v", got)
	}
	if v := got[0].Series[0].Values; !strings.HasSuffix(v, "$B$2:$B$3") {
		t.Errorf("series values = %q", v)
	}

	// A second chart on the same cell would sit on top of the first: refuse, and say why.
	err := editOps(ts, t, "b.xlsx", add("e2", "重复", "A1:B3"))
	if err == nil || !strings.Contains(err.Error(), "already anchored at E2") || !strings.Contains(err.Error(), "delete_chart") {
		t.Fatalf("duplicate anchor error = %v", err)
	}
	if got := readCharts(t, ts, "b.xlsx", "预算"); len(got) != 1 {
		t.Fatalf("a refused edit changed the file: %+v", got)
	}

	// Rebuild in one call: delete, then add the same spot again with the new range.
	if err := editOps(ts, t, "b.xlsx",
		map[string]any{"op": "delete_chart", "sheet": "预算", "cell": "E2"},
		add("E2", "构成", "A1:B4"),
		map[string]any{"op": "set_cell", "sheet": "预算", "cell": "D1", "value": "after"},
	); err != nil {
		t.Fatal(err)
	}
	got = readCharts(t, ts, "b.xlsx", "预算")
	if len(got) != 1 || !strings.HasSuffix(got[0].Series[0].Values, "$B$2:$B$4") {
		t.Fatalf("after rebuild: %+v", got)
	}

	if err := editOps(ts, t, "b.xlsx", map[string]any{"op": "delete_chart", "sheet": "预算", "cell": "$E$2"}); err != nil {
		t.Fatal(err)
	}
	if got := readCharts(t, ts, "b.xlsx", "预算"); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}

	// Nothing there to delete: the error lists what is.
	if err := editOps(ts, t, "b.xlsx", add("G2", "x", "A1:B3")); err != nil {
		t.Fatal(err)
	}
	err = editOps(ts, t, "b.xlsx", map[string]any{"op": "delete_chart", "sheet": "预算", "cell": "A9"})
	if err == nil || !strings.Contains(err.Error(), "no chart is anchored at A9") || !strings.Contains(err.Error(), "G2 pie") {
		t.Fatalf("missing chart error = %v", err)
	}
}

// excelize draws axis text as a pale #D9D9D9 unless told otherwise, which is close
// to unreadable on a white sheet. Charts made by add_chart name a legible color.
func TestAddChartAxisTextIsLegible(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "b.xlsx")
	if err := editOps(ts, t, "b.xlsx", map[string]any{"op": "add_chart", "sheet": "预算", "cell": "E2",
		"chart": map[string]any{"type": "column", "data_range": "A1:B3", "title": "t"}}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(root, "b.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var chartXML string
	for _, zf := range zr.File {
		if zf.Name != "xl/charts/chart1.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		chartXML = string(b)
	}
	if chartXML == "" {
		t.Fatal("xl/charts/chart1.xml is missing")
	}
	for _, axis := range []string{"catAx", "valAx"} {
		part := regexp.MustCompile(`(?s)<(?:c:)?` + axis + `>.*?</(?:c:)?` + axis + `>`).FindString(chartXML)
		if part == "" {
			t.Fatalf("no %s in the chart", axis)
		}
		if !strings.Contains(part, `srgbClr val="595959"`) {
			t.Errorf("%s text color is not set explicitly:\n%s", axis, part)
		}
	}
}

// Deleting one chart of a workbook that came from elsewhere keeps the others.
func TestEditXlsxDeletesOneForeignChart(t *testing.T) {
	root, ts := newXlsxTools(t)
	data, err := os.ReadFile(filepath.Join("testdata", "openpyxl-chart.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ext.xlsx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := editOps(ts, t, "ext.xlsx", map[string]any{"op": "delete_chart", "sheet": "预算", "cell": "E2"}); err != nil {
		t.Fatal(err)
	}
	if got := readCharts(t, ts, "ext.xlsx", "预算"); len(got) != 0 {
		t.Errorf("deleted chart still listed: %+v", got)
	}
	if got := readCharts(t, ts, "ext.xlsx", "趋势"); len(got) != 1 || got[0].Title != "覆盖率" {
		t.Errorf("the other sheet's chart was touched: %+v", got)
	}
}

// A chart added to a workbook from another producer lands on its own sheet and
// leaves the charts that were already there alone.
func TestEditXlsxAddsAChartToAForeignWorkbook(t *testing.T) {
	root, ts := newXlsxTools(t)
	data, err := os.ReadFile(filepath.Join("testdata", "openpyxl-chart.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ext.xlsx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := editOps(ts, t, "ext.xlsx", map[string]any{"op": "add_chart", "sheet": "无图", "cell": "B2",
		"chart": map[string]any{"type": "column", "data_range": "预算!A1:B3", "title": "新增"}}); err != nil {
		t.Fatal(err)
	}
	if got := readCharts(t, ts, "ext.xlsx", "无图"); len(got) != 1 || got[0].Title != "新增" || got[0].Anchor != "B2" {
		t.Errorf("the new chart: %+v", got)
	}
	if got := readCharts(t, ts, "ext.xlsx", "预算"); len(got) != 1 || got[0].Title != "预算构成" || !strings.HasSuffix(got[0].Series[0].Values, "$B$2:$B$3") {
		t.Errorf("the chart on 预算 changed: %+v", got)
	}
	if got := readCharts(t, ts, "ext.xlsx", "趋势"); len(got) != 1 || got[0].Title != "覆盖率" {
		t.Errorf("the chart on 趋势 changed: %+v", got)
	}
}

// excelize names a new chart part after the number of chart parts present; with a
// gap in the numbering that name belongs to another chart, so adding is refused.
func TestChartPartsFreeRefusesGappedNumbering(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := chartPartsFree(f); err != nil {
		t.Fatalf("empty workbook: %v", err)
	}
	f.Pkg.Store("xl/charts/chart1.xml", []byte("<x/>"))
	if err := chartPartsFree(f); err != nil {
		t.Fatalf("contiguous numbering: %v", err)
	}
	f.Pkg.Store("xl/charts/chart3.xml", []byte("<x/>")) // two parts, so the next name is chart3
	if err := chartPartsFree(f); err == nil || !strings.Contains(err.Error(), "chart3.xml") {
		t.Fatalf("gap in chart numbering: %v", err)
	}

	g := excelize.NewFile()
	defer g.Close()
	g.Pkg.Store("xl/drawings/drawing2.xml", []byte("<x/>")) // one part, so the next name is drawing2
	if err := chartPartsFree(g); err == nil || !strings.Contains(err.Error(), "drawing2.xml") {
		t.Fatalf("gap in drawing numbering: %v", err)
	}
}

// Absolute relationship targets (openpyxl, Apache POI) are made relative on open; the
// path arithmetic has to hold for every directory depth a package uses.
func TestRelativeTarget(t *testing.T) {
	for _, c := range []struct{ rels, target, want string }{
		{"xl/worksheets/_rels/sheet1.xml.rels", "/xl/drawings/drawing1.xml", "../drawings/drawing1.xml"},
		{"xl/drawings/_rels/drawing1.xml.rels", "/xl/charts/chart1.xml", "../charts/chart1.xml"},
		{"xl/_rels/workbook.xml.rels", "/xl/worksheets/sheet1.xml", "worksheets/sheet1.xml"},
		{"xl/_rels/workbook.xml.rels", "/xl/theme/theme1.xml", "theme/theme1.xml"},
		{"_rels/.rels", "/xl/workbook.xml", "xl/workbook.xml"},
		{"xl/worksheets/_rels/sheet1.xml.rels", "/xl/worksheets/sheet2.xml", "sheet2.xml"},
		{"xl/worksheets/_rels/sheet1.xml.rels", "/docProps/core.xml", "../../docProps/core.xml"},
	} {
		if got := relativeTarget(relSourceDir(c.rels), c.target); got != c.want {
			t.Errorf("%s -> %s = %q, want %q", c.rels, c.target, got, c.want)
		}
	}
}
