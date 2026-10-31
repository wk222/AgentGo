package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Charts are not part of excelize's read API, so they are read from the package
// itself: sheet -> drawing -> chart parts. This is what lets read_xlsx show the
// charts a workbook already has (so a model can tell whether one still matches
// its data) and lets edit_xlsx refuse a second chart on top of an existing one.

const maxChartsPerSheet = 20

// chartInfo is one chart on a worksheet.
type chartInfo struct {
	Index  int           `json:"index"`  // 1-based, in drawing order
	Anchor string        `json:"anchor"` // top-left cell, e.g. E2
	Type   string        `json:"type"`
	Title  string        `json:"title,omitempty"`
	Series []chartSeries `json:"series,omitempty"`
}

type chartSeries struct {
	Name       string `json:"name,omitempty"`
	Categories string `json:"categories,omitempty"`
	Values     string `json:"values,omitempty"`
}

type xAnchor struct {
	XMLName xml.Name
	From    *struct {
		Col int `xml:"col"`
		Row int `xml:"row"`
	} `xml:"from"`
	Frame *struct {
		Chart *struct {
			ID string `xml:"id,attr"`
		} `xml:"graphic>graphicData>chart"`
	} `xml:"graphicFrame"`
}

type xDrawing struct {
	Anchors []xAnchor `xml:",any"`
}

type xSeries struct {
	Name    string `xml:"tx>strRef>f"`
	Literal string `xml:"tx>v"`
	CatStr  string `xml:"cat>strRef>f"`
	CatNum  string `xml:"cat>numRef>f"`
	Val     string `xml:"val>numRef>f"`
	XVal    string `xml:"xVal>numRef>f"`
	XValStr string `xml:"xVal>strRef>f"`
	YVal    string `xml:"yVal>numRef>f"`
}

type xPlotGroup struct {
	XMLName xml.Name
	BarDir  *struct {
		Val string `xml:"val,attr"`
	} `xml:"barDir"`
	Grouping *struct {
		Val string `xml:"val,attr"`
	} `xml:"grouping"`
	Series []xSeries `xml:"ser"`
}

type xChartSpace struct {
	Title []struct {
		Text string `xml:"t"`
	} `xml:"chart>title>tx>rich>p>r"`
	Plot struct {
		Groups []xPlotGroup `xml:",any"` // barChart, lineChart, ...; axes and layout are skipped by name
	} `xml:"chart>plotArea"`
}

// chartsInFile lists the charts of every sheet of the workbook at abs.
func chartsInFile(abs string) (map[string][]chartInfo, error) {
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return chartsInZip(&zr.Reader)
}

// chartsInBytes lists the charts of every sheet of a serialised workbook.
func chartsInBytes(data []byte) (map[string][]chartInfo, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	return chartsInZip(zr)
}

// chartsOnSheet lists the charts a sheet has right now. excelize keeps drawings in
// memory until the workbook is written, so the open file is serialised to look.
func chartsOnSheet(f *excelize.File, sheet string) ([]chartInfo, error) {
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	book, err := chartsInBytes(buf.Bytes())
	if err != nil {
		return nil, err
	}
	return book[sheet], nil
}

func chartsInZip(zr *zip.Reader) (map[string][]chartInfo, error) {
	sheets, err := sheetFiles(zr)
	if err != nil {
		return nil, err
	}
	pkg := &pptxPackage{parts: map[string][]byte{}}
	var total uint64
	for _, zf := range zr.File {
		n := zf.Name
		wanted := strings.HasSuffix(n, ".rels") ||
			(strings.HasPrefix(n, "xl/drawings/") || strings.HasPrefix(n, "xl/charts/")) && strings.HasSuffix(n, ".xml")
		if !wanted {
			continue
		}
		total += zf.UncompressedSize64
		if zf.UncompressedSize64 > maxPartBytes || total > maxPptxBytes {
			return nil, fmt.Errorf("%s is too large to inspect its charts", n)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		pkg.parts[n] = b
	}
	out := map[string][]chartInfo{}
	for part, name := range sheets {
		for _, r := range pkg.rels(part) {
			if strings.HasSuffix(r.Type, "/drawing") {
				out[name] = append(out[name], pkg.drawingCharts(r.Target)...)
			}
		}
	}
	for name, list := range out {
		for i := range list {
			list[i].Index = i + 1
		}
		out[name] = list
	}
	return out, nil
}

// drawingCharts reads the chart anchors of one drawing part.
func (p *pptxPackage) drawingCharts(drawing string) []chartInfo {
	var doc xDrawing
	if xml.Unmarshal(p.parts[drawing], &doc) != nil {
		return nil
	}
	target := map[string]string{}
	for _, r := range p.rels(drawing) {
		target[r.ID] = r.Target
	}
	var out []chartInfo
	for _, a := range doc.Anchors {
		if a.From == nil || a.Frame == nil || a.Frame.Chart == nil {
			continue
		}
		cell, err := excelize.CoordinatesToCellName(a.From.Col+1, a.From.Row+1)
		if err != nil {
			continue
		}
		info := chartInfo{Anchor: cell, Type: "chart"}
		if part := target[a.Frame.Chart.ID]; part != "" {
			fillChartInfo(p.parts[part], &info)
		}
		out = append(out, info)
	}
	return out
}

// fillChartInfo fills type, title and data references from a chart part.
func fillChartInfo(data []byte, info *chartInfo) {
	var doc xChartSpace
	if xml.Unmarshal(data, &doc) != nil {
		return
	}
	var title []string
	for _, r := range doc.Title {
		title = append(title, r.Text)
	}
	info.Title = strings.Join(title, "")
	var kinds []string
	for _, g := range doc.Plot.Groups {
		if !strings.HasSuffix(g.XMLName.Local, "Chart") {
			continue
		}
		kinds = append(kinds, plotGroupKind(g))
		for _, s := range g.Series {
			if len(info.Series) >= 12 {
				break
			}
			info.Series = append(info.Series, chartSeries{
				Name:       firstNonEmpty(unquoteRef(s.Name), s.Literal),
				Categories: unquoteRef(firstNonEmpty(s.CatStr, firstNonEmpty(s.CatNum, s.XValStr))),
				Values:     unquoteRef(firstNonEmpty(s.Val, firstNonEmpty(s.YVal, s.XVal))),
			})
		}
	}
	if len(kinds) > 0 {
		info.Type = strings.Join(kinds, "+")
	}
}

// plotGroupKind names a plot group with the same words add_chart takes.
func plotGroupKind(g xPlotGroup) string {
	kind := strings.TrimSuffix(g.XMLName.Local, "Chart")
	kind = strings.TrimSuffix(kind, "3D")
	if kind != "bar" {
		return kind
	}
	dir := "column"
	if g.BarDir != nil && g.BarDir.Val == "bar" {
		dir = "bar"
	}
	if g.Grouping != nil && (g.Grouping.Val == "stacked" || g.Grouping.Val == "percentStacked") {
		return "stacked_" + dir
	}
	return dir
}

// unquoteRef drops the quotes Excel puts around sheet names: 'Data'!$A$1 -> Data!$A$1.
func unquoteRef(ref string) string {
	if i := strings.LastIndex(ref, "!"); i > 0 {
		return strings.Trim(ref[:i], "'") + ref[i:]
	}
	return ref
}

// readSheetCharts lists the charts of one sheet of the workbook at abs; a workbook
// whose package cannot be walked simply has none to show.
func readSheetCharts(abs, sheet string) []chartInfo {
	if _, err := os.Stat(abs); err != nil {
		return nil
	}
	book, err := chartsInFile(abs)
	if err != nil {
		return nil
	}
	list := book[sheet]
	if len(list) > maxChartsPerSheet {
		list = list[:maxChartsPerSheet]
	}
	return list
}

// describeChartList formats charts for an error message.
func describeChartList(list []chartInfo) string {
	if len(list) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(list))
	for _, c := range list {
		s := fmt.Sprintf("%s %s", c.Anchor, c.Type)
		if c.Title != "" {
			s += fmt.Sprintf(" %q", c.Title)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// chartPartsFree fails when excelize's next chart or drawing part name is already
// taken. It names a new chart part after how many chart parts the package holds
// (chartN+1) and a new drawing part the same way, which only works while the
// parts are numbered 1..N. A package with a gap (some producers leave one after a
// chart was removed) would have the new chart silently replace an existing one.
func chartPartsFree(f *excelize.File) error {
	var charts, drawings int
	names := map[string]bool{}
	note := func(k any) bool {
		name, _ := k.(string)
		names[name] = true
		return true
	}
	f.Pkg.Range(func(k, _ any) bool { return note(k) })
	f.Drawings.Range(func(k, _ any) bool { return note(k) })
	for name := range names {
		if strings.Contains(name, "xl/charts/chart") {
			charts++
		}
		if strings.Contains(name, "xl/drawings/drawing") {
			drawings++
		}
	}
	for _, next := range []string{
		fmt.Sprintf("xl/charts/chart%d.xml", charts+1),
		fmt.Sprintf("xl/drawings/drawing%d.xml", drawings+1),
	} {
		if names[next] {
			return fmt.Errorf("this workbook numbers its chart or drawing parts with a gap (%s already exists), so a new chart could overwrite an existing one; open and save it once in Excel or WPS, which renumbers them, then try again", next)
		}
	}
	return nil
}

// chartAnchorFree fails when a chart already sits on cell.
func chartAnchorFree(f *excelize.File, sheet, cell string) error {
	list, err := chartsOnSheet(f, sheet)
	if err != nil {
		return nil // cannot look: do not block the edit
	}
	for _, c := range list {
		if strings.EqualFold(c.Anchor, cell) {
			return fmt.Errorf("a chart is already anchored at %s on %q (%s); delete_chart it first or choose another cell", cell, sheet, describeChartList([]chartInfo{c}))
		}
	}
	return nil
}

// deleteChartAt removes the chart(s) anchored at cell.
func deleteChartAt(f *excelize.File, sheet, cell string) error {
	if err := cellArg(cell); err != nil {
		return err
	}
	cell = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(cell), "$", ""))
	list, err := chartsOnSheet(f, sheet)
	if err != nil {
		return err
	}
	found := false
	for _, c := range list {
		if strings.EqualFold(c.Anchor, cell) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no chart is anchored at %s on %q; charts on this sheet: %s (read_xlsx lists them)", cell, sheet, describeChartList(list))
	}
	return f.DeleteChart(sheet, cell)
}
