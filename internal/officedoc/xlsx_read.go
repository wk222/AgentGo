package officedoc

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Limits for one read_xlsx reply: the model reads the answer, so it must stay small.
const (
	defaultReadRows = 40
	maxReadRows     = 500
	maxReadCols     = 60
	maxReadCells    = 12000
	maxCellRunes    = 400
	maxFormulaList  = 100
	maxMatches      = 50
	maxXlsxBytes    = 64 << 20
)

type readXlsxInput struct {
	RelativePath string `json:"relative_path" jsonschema:"required,description=Workspace-relative path of an existing .xlsx workbook."`
	Sheet        string `json:"sheet,omitempty" jsonschema:"description=Sheet to read. Default: the first sheet. The reply always lists every sheet."`
	Range        string `json:"range,omitempty" jsonschema:"description=A1-style range such as A1:F40 (default: the whole used area). Use it to page through a large sheet."`
	MaxRows      int    `json:"max_rows,omitempty" jsonschema:"description=Maximum rows returned (default 40\\, max 500)."`
	Formulas     bool   `json:"formulas,omitempty" jsonschema:"description=Also list the formula text of the formula cells in the range. Values are always calculated."`
	Find         string `json:"find,omitempty" jsonschema:"description=Instead of reading a range: return the cells (in the given sheet or in all sheets) whose text contains this string."`
}

type sheetInfo struct {
	Name      string `json:"name"`
	Dimension string `json:"dimension,omitempty"`
	Hidden    bool   `json:"hidden,omitempty"`
}

type formulaCell struct {
	Cell    string `json:"cell"`
	Formula string `json:"formula"`
}

type matchCell struct {
	Sheet string `json:"sheet"`
	Cell  string `json:"cell"`
	Value any    `json:"value"`
}

type readXlsxOutput struct {
	Path      string        `json:"path"`
	Sheets    []sheetInfo   `json:"sheets"`
	Sheet     string        `json:"sheet,omitempty"`
	Range     string        `json:"range,omitempty"`
	Rows      [][]any       `json:"rows,omitempty"`
	Formulas  []formulaCell `json:"formulas,omitempty"`
	Matches   []matchCell   `json:"matches,omitempty"`
	Merged    []string      `json:"merged,omitempty"`
	Charts    []chartInfo   `json:"charts,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	Notes     []string      `json:"notes,omitempty"`
}

func readXlsx(env Env, in readXlsxInput) (readXlsxOutput, error) {
	f, rel, err := openWorkbook(env, in.RelativePath)
	if err != nil {
		return readXlsxOutput{}, err
	}
	defer f.Close()

	out := readXlsxOutput{Path: filepath.ToSlash(rel)}
	names := f.GetSheetList()
	if len(names) == 0 {
		return out, errors.New("the workbook has no sheets")
	}
	for i, n := range names {
		if i >= 64 {
			out.Notes = append(out.Notes, fmt.Sprintf("%d more sheets not listed", len(names)-i))
			break
		}
		dim, _ := f.GetSheetDimension(n)
		if dim == "A1" {
			dim = ""
		}
		out.Sheets = append(out.Sheets, sheetInfo{Name: n, Dimension: dim, Hidden: !isVisible(f, n)})
	}

	if strings.TrimSpace(in.Find) != "" {
		return findInWorkbook(f, names, in, out)
	}

	sheet, err := pickSheet(names, in.Sheet)
	if err != nil {
		return out, err
	}
	out.Sheet = sheet
	if abs, err := env.Resolve(rel); err == nil {
		out.Charts = readSheetCharts(abs, sheet)
	}

	cols, rows, err := usedExtent(f, sheet)
	if err != nil {
		return out, err
	}
	c1, r1, c2, r2 := 1, 1, cols, rows
	if strings.TrimSpace(in.Range) != "" {
		if c1, r1, c2, r2, err = parseRange(in.Range); err != nil {
			return out, fmt.Errorf("range %q: %w", in.Range, err)
		}
	}
	if cols == 0 || rows == 0 {
		out.Notes = append(out.Notes, "the sheet is empty")
		return out, nil
	}

	maxRows := in.MaxRows
	if maxRows <= 0 {
		maxRows = defaultReadRows
	}
	maxRows = min(maxRows, maxReadRows)
	if r2-r1+1 > maxRows {
		r2 = r1 + maxRows - 1
		out.Truncated = true
	}
	if c2-c1+1 > maxReadCols {
		c2 = c1 + maxReadCols - 1
		out.Truncated = true
	}
	for (r2-r1+1)*(c2-c1+1) > maxReadCells && r2 > r1 {
		r2--
		out.Truncated = true
	}
	lastCol, lastRow := cols, rows
	if strings.TrimSpace(in.Range) != "" {
		lastCol, lastRow = max(cols, c2), max(rows, r2)
	}

	var failed []string
	for r := r1; r <= r2; r++ {
		row := make([]any, 0, c2-c1+1)
		for c := c1; c <= c2; c++ {
			cell, _ := excelize.CoordinatesToCellName(c, r)
			v, formula, calcErr := readCell(f, sheet, cell)
			if formula != "" && in.Formulas && len(out.Formulas) < maxFormulaList {
				out.Formulas = append(out.Formulas, formulaCell{Cell: cell, Formula: "=" + formula})
			}
			if calcErr != "" && len(failed) < 5 {
				failed = append(failed, cell+": "+calcErr)
			}
			row = append(row, v)
		}
		out.Rows = append(out.Rows, row)
	}
	tl, _ := excelize.CoordinatesToCellName(c1, r1)
	br, _ := excelize.CoordinatesToCellName(c2, r2)
	out.Range = tl + ":" + br
	if out.Truncated {
		out.Notes = append(out.Notes, fmt.Sprintf("the sheet's used area ends at row %d, column %s; read more with range (for totals, rankings or comparisons use analyze_xlsx instead of reading everything)",
			lastRow, colName(lastCol)))
	}
	if len(failed) > 0 {
		out.Notes = append(out.Notes, "some formulas could not be calculated here (Excel will calculate them on open): "+strings.Join(failed, "; "))
	}
	if merged, err := f.GetMergeCells(sheet); err == nil {
		for i, m := range merged {
			if i >= 30 {
				break
			}
			out.Merged = append(out.Merged, m.GetStartAxis()+":"+m.GetEndAxis())
		}
	}
	return out, nil
}

// openWorkbook resolves and opens an existing .xlsx inside the workspace.
func openWorkbook(env Env, relPath string) (*excelize.File, string, error) {
	rel := strings.TrimSpace(relPath)
	if rel == "" {
		return nil, "", errors.New("relative_path is required")
	}
	if !strings.EqualFold(filepath.Ext(rel), ".xlsx") {
		return nil, "", errors.New("only .xlsx workbooks are supported (save .xls/.csv as .xlsx first)")
	}
	abs, err := env.Resolve(rel)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	if st.IsDir() {
		return nil, "", fmt.Errorf("%s is a directory", filepath.Base(abs))
	}
	if st.Size() > maxXlsxBytes {
		return nil, "", fmt.Errorf("%s is too large: %d bytes (max %d)", filepath.Base(abs), st.Size(), maxXlsxBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	if fixed, changed, err := relativeRelTargets(data); err == nil && changed {
		data = fixed
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("cannot open %s as a workbook: %w", filepath.Base(abs), err)
	}
	return f, rel, nil
}

func isVisible(f *excelize.File, sheet string) bool {
	v, err := f.GetSheetVisible(sheet)
	return err != nil || v
}

// pickSheet resolves a sheet name (case-insensitively); empty means the first.
func pickSheet(names []string, want string) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return names[0], nil
	}
	for _, n := range names {
		if n == want {
			return n, nil
		}
	}
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return n, nil
		}
	}
	return "", fmt.Errorf("no sheet named %q; sheets: %s", want, strings.Join(names, ", "))
}

// usedExtent returns the number of columns and rows that hold cells. The
// <dimension> tag is not trusted on its own: excelize leaves it at A1, and
// other generators get it wrong.
func usedExtent(f *excelize.File, sheet string) (cols, rows int, err error) {
	all, err := f.GetRows(sheet)
	if err != nil {
		return 0, 0, err
	}
	rows = len(all)
	for rows > 0 && len(all[rows-1]) == 0 {
		rows--
	}
	for _, r := range all {
		cols = max(cols, len(r))
	}
	if dim, _ := f.GetSheetDimension(sheet); strings.Contains(dim, ":") {
		if _, _, c2, r2, e := parseRange(dim); e == nil {
			cols, rows = max(cols, c2), max(rows, r2)
		}
	}
	return cols, rows, nil
}

func parseRange(s string) (c1, r1, c2, r2 int, err error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "$", "")
	a, b, found := strings.Cut(s, ":")
	if !found {
		b = a
	}
	if c1, r1, err = excelize.CellNameToCoordinates(strings.TrimSpace(a)); err != nil {
		return
	}
	if c2, r2, err = excelize.CellNameToCoordinates(strings.TrimSpace(b)); err != nil {
		return
	}
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	return
}

func colName(n int) string {
	s, err := excelize.ColumnNumberToName(max(n, 1))
	if err != nil {
		return strconv.Itoa(n)
	}
	return s
}

// readCell returns a cell as JSON-friendly data: numbers stay numbers, booleans
// booleans, text text, and anything with a display format (dates, percentages,
// currency) as the text a user would see. For a formula it also returns the
// formula text, and a calculation error message when the value is unknown.
func readCell(f *excelize.File, sheet, cell string) (value any, formula, calcErr string) {
	disp, _ := f.GetCellValue(sheet, cell)
	formula, _ = f.GetCellFormula(sheet, cell)
	if formula == "" {
		if disp == "" {
			return "", "", ""
		}
		return plainCell(f, sheet, cell, disp), "", ""
	}
	res, err := f.CalcCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	switch {
	case err != nil && isExcelError(err.Error()):
		return err.Error(), formula, ""
	case err != nil && disp != "":
		return scalar(disp, disp), formula, "" // the result stored by the program that wrote the file
	case err != nil:
		return "#?", formula, err.Error()
	}
	return scalar(res, disp), formula, ""
}

// plainCell converts a non-formula cell with display text disp. excelize reports
// a plain number (no t attribute) as CellTypeUnset, so Unset is numeric here.
func plainCell(f *excelize.File, sheet, cell, disp string) any {
	typ, _ := f.GetCellType(sheet, cell)
	switch typ {
	case excelize.CellTypeBool:
		return strings.EqualFold(disp, "true") || disp == "1"
	case excelize.CellTypeUnset, excelize.CellTypeNumber, excelize.CellTypeDate:
		raw, _ := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		return scalar(raw, disp)
	}
	return clip(disp)
}

// scalar prefers the raw number when the display text is just that number, and
// the display text when a number format changes how it reads.
func scalar(raw, disp string) any {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return clip(firstNonEmpty(disp, raw))
	}
	if disp == "" || disp == raw {
		return number(n)
	}
	if dn, err := strconv.ParseFloat(disp, 64); err == nil && dn == n {
		return number(n)
	}
	return clip(disp)
}

func number(n float64) any {
	if n == math.Trunc(n) && math.Abs(n) < 1e15 {
		return int64(n)
	}
	return n
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxCellRunes {
		return s
	}
	return string(r[:maxCellRunes]) + fmt.Sprintf("…(+%d chars)", len(r)-maxCellRunes)
}

func findInWorkbook(f *excelize.File, names []string, in readXlsxInput, out readXlsxOutput) (readXlsxOutput, error) {
	targets := names
	if strings.TrimSpace(in.Sheet) != "" {
		s, err := pickSheet(names, in.Sheet)
		if err != nil {
			return out, err
		}
		targets = []string{s}
	}
	for _, name := range targets {
		hits, err := f.SearchSheet(name, strings.TrimSpace(in.Find))
		if err != nil {
			return out, err
		}
		for _, cell := range hits {
			if len(out.Matches) >= maxMatches {
				out.Truncated = true
				return out, nil
			}
			v, _, _ := readCell(f, name, cell)
			out.Matches = append(out.Matches, matchCell{Sheet: name, Cell: cell, Value: v})
		}
	}
	if len(out.Matches) == 0 {
		out.Notes = append(out.Notes, "no cell contains that text")
	}
	return out, nil
}
