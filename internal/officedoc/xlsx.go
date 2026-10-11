package officedoc

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// Sheet is one worksheet. Rows hold JSON-ish values: numbers and booleans are
// stored as such, strings as text, nil as an empty cell, and a string starting
// with "=" as a formula.
type Sheet struct {
	Name   string
	Rows   [][]any
	Header bool // style the first row, freeze it and add a filter
}

// Limits keep one tool call from producing an absurd file.
const (
	MaxSheets     = 32
	MaxSheetRows  = 100000
	MaxSheetCols  = 200
	maxColumnChar = 60
)

// WriteXlsx writes the workbook. At least one sheet is required.
func WriteXlsx(w io.Writer, sheets []Sheet) error {
	if len(sheets) == 0 {
		return fmt.Errorf("a workbook needs at least one sheet")
	}
	if len(sheets) > MaxSheets {
		return fmt.Errorf("too many sheets: %d (max %d)", len(sheets), MaxSheets)
	}
	f := excelize.NewFile()
	defer f.Close()

	headStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D9E2F3"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return err
	}

	used := map[string]bool{}
	for i, s := range sheets {
		name := uniqueSheetName(s.Name, i, used)
		if i == 0 {
			if err := f.SetSheetName(f.GetSheetName(0), name); err != nil {
				return err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return err
		}
		if err := fillSheet(f, name, s, headStyle); err != nil {
			return fmt.Errorf("sheet %q: %w", name, err)
		}
	}
	f.SetActiveSheet(0)
	return f.Write(w)
}

func fillSheet(f *excelize.File, name string, s Sheet, headStyle int) error {
	if len(s.Rows) > MaxSheetRows {
		return fmt.Errorf("too many rows: %d (max %d)", len(s.Rows), MaxSheetRows)
	}
	cols := 0
	widths := map[int]float64{}
	for ri, row := range s.Rows {
		if len(row) > MaxSheetCols {
			return fmt.Errorf("row %d has %d columns (max %d)", ri+1, len(row), MaxSheetCols)
		}
		if len(row) > cols {
			cols = len(row)
		}
		for ci, v := range row {
			cell, err := excelize.CoordinatesToCellName(ci+1, ri+1)
			if err != nil {
				return err
			}
			if str, ok := v.(string); ok && strings.HasPrefix(str, "=") && len(str) > 1 {
				err = f.SetCellFormula(name, cell, strings.TrimPrefix(str, "=")) // the file format stores formulas without the "="
			} else {
				err = f.SetCellValue(name, cell, cleanCell(v))
			}
			if err != nil {
				return fmt.Errorf("%s: %w", cell, err)
			}
			if w := displayWidth(v); w > widths[ci] {
				widths[ci] = w
			}
		}
	}
	for ci, w := range widths {
		col, _ := excelize.ColumnNumberToName(ci + 1)
		if w < 8 {
			w = 8
		}
		if w > maxColumnChar {
			w = maxColumnChar
		}
		if err := f.SetColWidth(name, col, col, w+2); err != nil {
			return err
		}
	}
	if s.Header && len(s.Rows) > 0 && cols > 0 {
		last, _ := excelize.CoordinatesToCellName(cols, 1)
		if err := f.SetCellStyle(name, "A1", last, headStyle); err != nil {
			return err
		}
		if err := f.SetPanes(name, &excelize.Panes{
			Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
			Selection: []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}},
		}); err != nil {
			return err
		}
		end, _ := excelize.CoordinatesToCellName(cols, len(s.Rows))
		if len(s.Rows) > 1 {
			if err := f.AutoFilter(name, "A1:"+end, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanCell drops control characters XML cannot carry from string values.
func cleanCell(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			return r
		}
		return -1
	}, s)
}

// displayWidth estimates how many characters wide a cell prints; CJK counts double.
func displayWidth(v any) float64 {
	if v == nil {
		return 0
	}
	var w float64
	longest := 0.0
	for _, r := range fmt.Sprint(v) {
		switch {
		case r == '\n':
			if w > longest {
				longest = w
			}
			w = 0
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) || r >= 0xFF00:
			w += 2
		default:
			w++
		}
	}
	if w > longest {
		longest = w
	}
	return longest
}

// uniqueSheetName makes a name Excel accepts: at most 31 characters, none of
// []:*?/\ , not blank, and unique case-insensitively within the workbook.
func uniqueSheetName(raw string, index int, used map[string]bool) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) || r < 0x20 {
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	name = strings.Trim(name, "'")
	if name == "" {
		name = fmt.Sprintf("Sheet%d", index+1)
	}
	name = truncateRunes(name, 31)
	base := name
	for n := 2; used[strings.ToLower(name)]; n++ {
		suffix := fmt.Sprintf("_%d", n)
		name = truncateRunes(base, 31-len(suffix)) + suffix
	}
	used[strings.ToLower(name)] = true
	return name
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
