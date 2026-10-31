package officedoc

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

type pptxChartIn struct {
	Type       string         `json:"type" jsonschema:"required,description=column | stacked_column | bar (horizontal) | line | pie | doughnut."`
	Categories []string       `json:"categories" jsonschema:"required,description=Category labels: the x axis or the slices."`
	Series     []pptxSeriesIn `json:"series" jsonschema:"required,description=Data series. Pie and doughnut use only the first."`
	Unit       string         `json:"unit,omitempty" jsonschema:"description=Unit written on the value axis\\, e.g. 万元 or %."`
	Labels     *bool          `json:"labels,omitempty" jsonschema:"description=Print the values on the chart. Default: on for up to about 24 points."`
}

type pptxSeriesIn struct {
	Name   string    `json:"name" jsonschema:"required,description=Series name (legend)."`
	Values []float64 `json:"values" jsonschema:"required,description=One number per category."`
}

// chartPart is a finished chart and the workbook behind its "Edit Data".
type chartPart struct {
	xml      string
	workbook []byte
}

const (
	maxChartCats   = 60
	maxChartSeries = 8
	nsC            = `xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"`
	relPackage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/package"
)

type chartKind struct {
	tag      string // barChart, lineChart, pieChart, doughnutChart
	barDir   string // col or bar
	grouping string
	pie      bool
}

var pptxChartKinds = map[string]chartKind{
	"column":         {tag: "barChart", barDir: "col", grouping: "clustered"},
	"stacked_column": {tag: "barChart", barDir: "col", grouping: "stacked"},
	"bar":            {tag: "barChart", barDir: "bar", grouping: "clustered"},
	"line":           {tag: "lineChart", grouping: "standard"},
	"pie":            {tag: "pieChart", pie: true},
	"doughnut":       {tag: "doughnutChart", pie: true},
}

func (d *deck) chartSlide(in pptxSlideIn) (*slide, error) {
	c := in.Chart
	if c == nil {
		return nil, errors.New("chart is required")
	}
	kind, ok := pptxChartKinds[strings.ToLower(strings.TrimSpace(c.Type))]
	if !ok {
		return nil, fmt.Errorf("chart type %q must be one of column, stacked_column, bar, line, pie, doughnut", c.Type)
	}
	if n := len(c.Categories); n < 1 || n > maxChartCats {
		return nil, fmt.Errorf("categories needs 1 to %d labels, got %d", maxChartCats, n)
	}
	if n := len(c.Series); n < 1 || n > maxChartSeries {
		return nil, fmt.Errorf("series needs 1 to %d entries, got %d", maxChartSeries, n)
	}
	series := c.Series
	if kind.pie && len(series) > 1 {
		d.warn("a %s chart shows one series; only %q is used", c.Type, series[0].Name)
		series = series[:1]
	}
	for i := range series {
		if len(series[i].Values) != len(c.Categories) {
			return nil, fmt.Errorf("series %q has %d values but there are %d categories", series[i].Name, len(series[i].Values), len(c.Categories))
		}
		for _, v := range series[i].Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("series %q holds a value that is not a finite number", series[i].Name)
			}
		}
		if strings.TrimSpace(series[i].Name) == "" {
			series[i].Name = fmt.Sprintf("系列%d", i+1)
		}
	}
	if len(in.Takeaway) > 4 {
		return nil, errors.New("takeaway takes at most 4 points")
	}
	labels := len(c.Categories)*len(series) <= 24
	if c.Labels != nil {
		labels = *c.Labels
	}
	if kind.pie && len(c.Categories) > 8 {
		d.warn("a pie with %d slices is hard to read; consider a bar chart", len(c.Categories))
	}

	part, err := d.buildChart(kind, c.Categories, series, strings.TrimSpace(c.Unit), labels)
	if err != nil {
		return nil, err
	}
	d.charts = append(d.charts, part)
	n := len(d.charts)

	s := d.contentSlide(layoutTitleOnly, in.Title, in.Notes)
	w, h := d.fr.w, d.fr.h-0.1
	hasPanel := len(in.Takeaway) > 0
	if hasPanel {
		w = 7.7
	}
	rid := s.addRel(relChart, fmt.Sprintf("../charts/chart%d.xml", n))
	id := s.id()
	names := make([]string, len(series))
	for i, sr := range series {
		names[i] = sr.Name
	}
	alt := fmt.Sprintf("%s: %s chart of %s", in.Title, c.Type, strings.Join(names, ", "))
	fmt.Fprintf(&s.shapes, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Chart %d" descr="%s"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`+
		`<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">`+
		`<c:chart %s r:id="%s"/></a:graphicData></a:graphic></p:graphicFrame>`,
		id, id, escapeXML(alt), emu(d.fr.x), emu(d.fr.y), emu(w), emu(h), nsC, rid)

	if hasPanel {
		px := d.fr.x + w + 0.3
		pw := d.fr.w - w - 0.3
		tb := textBlock{widthIn: pw, heightIn: h, insetX: 0.3, insetY: 0.3}
		ps, size, fit := fitParas(tb, []float64{20, 18, 16, 14}, func(sz float64) []para { return bulletParas(in.Takeaway, sz, d.pal) })
		if !fit {
			d.warn("the takeaway does not fit beside the chart even at %.0f pt; shorten it", size)
		}
		s.box(px, d.fr.y, pw, h, ps, boxOpts{name: "Takeaway", fill: d.pal.Surface, anchor: "ctr", insetX: 0.3, insetY: 0.3})
		s.deco("Deco takeaway bar", px, d.fr.y, 0.08, h, d.pal.Accent, 0, "rect")
	}
	return s.finish(), nil
}

func (d *deck) buildChart(kind chartKind, cats []string, series []pptxSeriesIn, unit string, labels bool) (chartPart, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Sheet1"
	for i, sr := range series {
		col, _ := excelize.ColumnNumberToName(i + 2)
		_ = f.SetCellValue(sheet, col+"1", cleanCell(sr.Name))
	}
	for r, cat := range cats {
		_ = f.SetCellValue(sheet, "A"+strconv.Itoa(r+2), cleanCell(cat))
		for i, sr := range series {
			col, _ := excelize.ColumnNumberToName(i + 2)
			_ = f.SetCellValue(sheet, col+strconv.Itoa(r+2), sr.Values[r])
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return chartPart{}, err
	}
	return chartPart{xml: d.chartXML(kind, cats, series, unit, labels), workbook: buf.Bytes()}, nil
}

func colorOf(pal palette, i int) string { return pal.Chart[i%len(pal.Chart)] }

func (d *deck) chartXML(kind chartKind, cats []string, series []pptxSeriesIn, unit string, labels bool) string {
	pal := d.pal
	n := len(cats)
	catRef := fmt.Sprintf("Sheet1!$A$2:$A$%d", n+1)
	var catCache strings.Builder
	fmt.Fprintf(&catCache, `<c:ptCount val="%d"/>`, n)
	for i, c := range cats {
		fmt.Fprintf(&catCache, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, escapeXML(c))
	}
	catXML := `<c:cat><c:strRef><c:f>` + catRef + `</c:f><c:strCache>` + catCache.String() + `</c:strCache></c:strRef></c:cat>`

	txPr := func(size int, color string, bold bool) string {
		b := "0"
		if bold {
			b = "1"
		}
		return fmt.Sprintf(`<c:txPr><a:bodyPr/><a:lstStyle/><a:p><a:pPr><a:defRPr sz="%d" b="%s"><a:solidFill><a:srgbClr val="%s"/></a:solidFill>%s</a:defRPr></a:pPr><a:endParaRPr lang="zh-CN"/></a:p></c:txPr>`,
			size*100, b, color, d.fontXML())
	}
	dLbls := func() string {
		if !labels {
			return ""
		}
		s := `<c:dLbls>`
		if kind.pie {
			s += `<c:numFmt formatCode="0%" sourceLinked="0"/>`
		}
		s += `<c:spPr><a:noFill/><a:ln><a:noFill/></a:ln></c:spPr>` + txPr(12, pal.Text, false)
		switch {
		case kind.tag == "doughnutChart":
		case kind.pie:
			s += `<c:dLblPos val="outEnd"/>`
		case kind.tag == "lineChart":
			s += `<c:dLblPos val="t"/>`
		case kind.grouping == "stacked":
			s += `<c:dLblPos val="ctr"/>`
		default:
			s += `<c:dLblPos val="outEnd"/>`
		}
		pct, val := "0", "1"
		if kind.pie {
			pct, val = "1", "0"
		}
		return s + `<c:showLegendKey val="0"/><c:showVal val="` + val + `"/><c:showCatName val="0"/><c:showSerName val="0"/><c:showPercent val="` + pct + `"/><c:showBubbleSize val="0"/></c:dLbls>`
	}

	var sers strings.Builder
	for i, sr := range series {
		col, _ := excelize.ColumnNumberToName(i + 2)
		var vals strings.Builder
		fmt.Fprintf(&vals, `<c:ptCount val="%d"/>`, n)
		for j, v := range sr.Values {
			fmt.Fprintf(&vals, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, j, strconv.FormatFloat(v, 'f', -1, 64))
		}
		tx := fmt.Sprintf(`<c:tx><c:strRef><c:f>Sheet1!$%s$1</c:f><c:strCache><c:ptCount val="1"/><c:pt idx="0"><c:v>%s</c:v></c:pt></c:strCache></c:strRef></c:tx>`, col, escapeXML(sr.Name))
		valXML := fmt.Sprintf(`<c:val><c:numRef><c:f>Sheet1!$%s$2:$%s$%d</c:f><c:numCache><c:formatCode>General</c:formatCode>%s</c:numCache></c:numRef></c:val>`, col, col, n+1, vals.String())
		color := colorOf(pal, i)
		fmt.Fprintf(&sers, `<c:ser><c:idx val="%d"/><c:order val="%d"/>%s`, i, i, tx)
		switch {
		case kind.pie:
			for j := 0; j < n; j++ {
				fmt.Fprintf(&sers, `<c:dPt><c:idx val="%d"/><c:bubble3D val="0"/><c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln w="19050"><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:ln></c:spPr></c:dPt>`, j, colorOf(pal, j))
			}
			sers.WriteString(dLbls() + catXML + valXML)
		case kind.tag == "lineChart":
			fmt.Fprintf(&sers, `<c:spPr><a:ln w="31750" cap="rnd"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:round/></a:ln></c:spPr>`+
				`<c:marker><c:symbol val="circle"/><c:size val="7"/><c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln w="9525"><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:ln></c:spPr></c:marker>`, color, color)
			sers.WriteString(dLbls() + catXML + valXML + `<c:smooth val="0"/>`)
		default:
			fmt.Fprintf(&sers, `<c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill></c:spPr><c:invertIfNegative val="0"/>`, color)
			sers.WriteString(dLbls() + catXML + valXML)
		}
		sers.WriteString(`</c:ser>`)
	}

	var plot string
	switch kind.tag {
	case "barChart":
		overlap := ""
		if kind.grouping == "stacked" {
			overlap = `<c:overlap val="100"/>`
		}
		plot = `<c:barChart><c:barDir val="` + kind.barDir + `"/><c:grouping val="` + kind.grouping + `"/><c:varyColors val="0"/>` + sers.String() +
			`<c:gapWidth val="70"/>` + overlap + `<c:axId val="50010"/><c:axId val="50020"/></c:barChart>`
	case "lineChart":
		plot = `<c:lineChart><c:grouping val="standard"/><c:varyColors val="0"/>` + sers.String() + `<c:marker val="1"/><c:axId val="50010"/><c:axId val="50020"/></c:lineChart>`
	case "pieChart":
		plot = `<c:pieChart><c:varyColors val="1"/>` + sers.String() + `<c:firstSliceAng val="0"/></c:pieChart>`
	default:
		plot = `<c:doughnutChart><c:varyColors val="1"/>` + sers.String() + `<c:firstSliceAng val="0"/><c:holeSize val="55"/></c:doughnutChart>`
	}

	if !kind.pie {
		horizontal := kind.barDir == "bar"
		catPos, valPos, rot := "b", "l", "-5400000"
		catOrient, valCross := "minMax", "autoZero"
		if horizontal {
			catPos, valPos, rot = "l", "b", "0"
			catOrient, valCross = "maxMin", "max" // first category on top
		}
		axisLine := fmt.Sprintf(`<c:spPr><a:ln w="9525"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:ln></c:spPr>`, pal.Line)
		title := ""
		if unit != "" {
			title = `<c:title><c:tx><c:rich><a:bodyPr rot="` + rot + `" vert="horz"/><a:lstStyle/><a:p><a:pPr><a:defRPr sz="1200" b="0"/></a:pPr>` +
				`<a:r><a:rPr lang="zh-CN" sz="1200" b="0"><a:solidFill><a:srgbClr val="` + pal.Muted + `"/></a:solidFill>` + d.fontXML() + `</a:rPr><a:t>` + escapeXML(unit) + `</a:t></a:r></a:p></c:rich></c:tx><c:overlay val="0"/></c:title>`
		}
		plot += `<c:catAx><c:axId val="50010"/><c:scaling><c:orientation val="` + catOrient + `"/></c:scaling><c:delete val="0"/><c:axPos val="` + catPos + `"/>` +
			`<c:numFmt formatCode="General" sourceLinked="0"/><c:majorTickMark val="none"/><c:minorTickMark val="none"/><c:tickLblPos val="nextTo"/>` + axisLine + txPr(12, pal.Text, false) +
			`<c:crossAx val="50020"/><c:crosses val="autoZero"/><c:auto val="1"/><c:lblAlgn val="ctr"/><c:lblOffset val="100"/><c:noMultiLvlLbl val="0"/></c:catAx>` +
			`<c:valAx><c:axId val="50020"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="` + valPos + `"/>` +
			`<c:majorGridlines><c:spPr><a:ln w="6350"><a:solidFill><a:srgbClr val="` + pal.Line + `"/></a:solidFill></a:ln></c:spPr></c:majorGridlines>` + title +
			`<c:numFmt formatCode="General" sourceLinked="0"/><c:majorTickMark val="none"/><c:minorTickMark val="none"/><c:tickLblPos val="nextTo"/>` +
			`<c:spPr><a:ln><a:noFill/></a:ln></c:spPr>` + txPr(12, pal.Muted, false) +
			`<c:crossAx val="50010"/><c:crosses val="` + valCross + `"/><c:crossBetween val="between"/></c:valAx>`
	}

	legend := ""
	if len(series) > 1 || kind.pie {
		pos := "b"
		if kind.pie {
			pos = "r"
		}
		legend = `<c:legend><c:legendPos val="` + pos + `"/><c:overlay val="0"/>` + txPr(12, pal.Text, false) + `</c:legend>`
	}
	return xmlHeader + `<c:chartSpace ` + nsC + ` ` + nsA + ` ` + nsR + `><c:date1904 val="0"/><c:roundedCorners val="0"/>` +
		`<c:chart><c:autoTitleDeleted val="1"/><c:plotArea><c:layout/>` + plot + `</c:plotArea>` + legend +
		`<c:plotVisOnly val="1"/><c:dispBlanksAs val="gap"/></c:chart>` +
		`<c:spPr><a:noFill/><a:ln><a:noFill/></a:ln></c:spPr>` + txPr(12, pal.Text, false) +
		`<c:externalData r:id="rId1"><c:autoUpdate val="0"/></c:externalData></c:chartSpace>`
}
