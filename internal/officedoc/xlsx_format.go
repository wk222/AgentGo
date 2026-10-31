package officedoc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// applyFormat merges fm into the style of every cell in rng, keeping whatever
// styling those cells already have. Cells that share a style are set in one run.
func applyFormat(f *excelize.File, sheet, rng string, fm *xlsxFormat) error {
	c1, r1, c2, r2, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("range %q: %w", rng, err)
	}
	if (c2-c1+1)*(r2-r1+1) > maxFormatCells {
		return fmt.Errorf("range %q is too large (max %d cells)", rng, maxFormatCells)
	}
	var fill, fontColor string
	if fm.Fill != "" {
		if fill, err = normalizeColor(fm.Fill); err != nil {
			return err
		}
	}
	if fm.FontColor != "" {
		if fontColor, err = normalizeColor(fm.FontColor); err != nil {
			return err
		}
	}
	switch strings.ToLower(fm.Align) {
	case "", "left", "center", "right":
	default:
		return fmt.Errorf("align %q must be left, center or right", fm.Align)
	}
	if fm.Bold == nil && fm.Italic == nil && fm.FontSize == 0 && fontColor == "" && fill == "" &&
		fm.NumFmt == "" && fm.Align == "" && fm.Wrap == nil && fm.Border == nil {
		return errors.New("format is empty")
	}

	made := map[int]int{} // existing style id -> merged style id
	styleFor := func(old int) (int, error) {
		if id, ok := made[old]; ok {
			return id, nil
		}
		base := &excelize.Style{}
		if old != 0 {
			if s, err := f.GetStyle(old); err == nil && s != nil {
				base = sanitizeStyle(s)
			}
		}
		mergeFormat(base, fm, fill, fontColor)
		id, err := f.NewStyle(base)
		if err != nil {
			return 0, err
		}
		made[old] = id
		return id, nil
	}

	for r := r1; r <= r2; r++ {
		start, runID := 0, -1
		flush := func(end int) error {
			if runID < 0 || start == 0 {
				return nil
			}
			tl, _ := excelize.CoordinatesToCellName(start, r)
			br, _ := excelize.CoordinatesToCellName(end, r)
			return f.SetCellStyle(sheet, tl, br, runID)
		}
		for c := c1; c <= c2; c++ {
			cell, _ := excelize.CoordinatesToCellName(c, r)
			old, err := f.GetCellStyle(sheet, cell)
			if err != nil {
				return err
			}
			id, err := styleFor(old)
			if err != nil {
				return err
			}
			if id != runID {
				if err := flush(c - 1); err != nil {
					return err
				}
				start, runID = c, id
			}
		}
		if err := flush(c2); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeStyle makes a style read back from a file acceptable to NewStyle.
// Workbooks written by openpyxl and Excel carry an empty <patternFill/> for "no
// fill", which GetStyle reports as pattern -1 and NewStyle then refuses, so
// every format on an already styled cell would fail.
func sanitizeStyle(s *excelize.Style) *excelize.Style {
	if s.Fill.Pattern < 0 || s.Fill.Pattern > 18 {
		s.Fill = excelize.Fill{}
	}
	if s.Fill.Type == "" && s.Fill.Pattern != 0 {
		s.Fill.Pattern = 0
	}
	return s
}

func mergeFormat(base *excelize.Style, fm *xlsxFormat, fill, fontColor string) {
	if fm.Bold != nil || fm.Italic != nil || fm.FontSize > 0 || fontColor != "" {
		if base.Font == nil {
			base.Font = &excelize.Font{}
		}
		if fm.Bold != nil {
			base.Font.Bold = *fm.Bold
		}
		if fm.Italic != nil {
			base.Font.Italic = *fm.Italic
		}
		if fm.FontSize > 0 {
			base.Font.Size = fm.FontSize
		}
		if fontColor != "" {
			base.Font.Color = fontColor
		}
	}
	if fill != "" {
		base.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill}}
	}
	if fm.NumFmt != "" {
		code := fm.NumFmt
		base.CustomNumFmt = &code
		base.NumFmt = 0
	}
	if fm.Align != "" || fm.Wrap != nil {
		if base.Alignment == nil {
			base.Alignment = &excelize.Alignment{}
		}
		if fm.Align != "" {
			base.Alignment.Horizontal = strings.ToLower(fm.Align)
		}
		if fm.Wrap != nil {
			base.Alignment.WrapText = *fm.Wrap
			base.Alignment.Vertical = "top"
		}
	}
	if fm.Border != nil {
		if !*fm.Border {
			base.Border = nil
			return
		}
		for _, side := range []string{"left", "top", "right", "bottom"} {
			base.Border = append(base.Border, excelize.Border{Type: side, Color: "#BFBFBF", Style: 1})
		}
	}
}

var chartKinds = map[string]excelize.ChartType{
	"column":         excelize.Col,
	"stacked_column": excelize.ColStacked,
	"bar":            excelize.Bar,
	"line":           excelize.Line,
	"area":           excelize.Area,
	"pie":            excelize.Pie,
	"doughnut":       excelize.Doughnut,
	"scatter":        excelize.Scatter,
}

// addChart places a chart whose series come from a header row and a category
// column: every column after the first is one series.
func addChart(f *excelize.File, sheet string, op xlsxOp) error {
	ch := op.Chart
	if ch == nil {
		return errors.New("chart is required")
	}
	if err := cellArg(op.Cell); err != nil {
		return err
	}
	kind, ok := chartKinds[strings.ToLower(strings.TrimSpace(ch.Type))]
	if !ok {
		return fmt.Errorf("chart type %q must be one of column, stacked_column, bar, line, area, pie, doughnut, scatter", ch.Type)
	}
	dataSheet, rng := sheet, strings.TrimSpace(ch.DataRange)
	if i := strings.LastIndex(rng, "!"); i >= 0 {
		name, err := pickSheet(f.GetSheetList(), strings.Trim(rng[:i], "'"))
		if err != nil {
			return err
		}
		dataSheet, rng = name, rng[i+1:]
	}
	c1, r1, c2, r2, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("data_range %q: %w", ch.DataRange, err)
	}
	if c2-c1 < 1 || r2-r1 < 1 {
		return errors.New("data_range needs a header row, a category column and at least one series column")
	}
	ref := "'" + strings.ReplaceAll(dataSheet, "'", "''") + "'!"
	abs := func(c, r int) string { return "$" + colName(c) + "$" + strconv.Itoa(r) }
	cats := ref + abs(c1, r1+1) + ":" + abs(c1, r2)
	var series []excelize.ChartSeries
	for c := c1 + 1; c <= c2; c++ {
		series = append(series, excelize.ChartSeries{
			Name:       ref + abs(c, r1),
			Categories: cats,
			Values:     ref + abs(c, r1+1) + ":" + abs(c, r2),
		})
	}
	// excelize's own axis text is a pale #D9D9D9, close to unreadable on a white
	// sheet; use the mid-gray Excel draws itself, and gridlines to read values off.
	axisText := excelize.Font{Color: "595959", Size: 10}
	chart := &excelize.Chart{
		Type:      kind,
		Series:    series,
		Dimension: excelize.ChartDimension{Width: 640, Height: 360},
		Legend:    excelize.ChartLegend{Position: "bottom"},
		XAxis:     excelize.ChartAxis{Font: axisText},
		YAxis:     excelize.ChartAxis{Font: axisText, MajorGridLines: true},
	}
	if t := strings.TrimSpace(ch.Title); t != "" {
		chart.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: t}}}
	}
	anchor := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(op.Cell), "$", ""))
	if err := chartAnchorFree(f, sheet, anchor); err != nil {
		return err
	}
	if err := chartPartsFree(f); err != nil {
		return err
	}
	return f.AddChart(sheet, anchor, chart)
}
