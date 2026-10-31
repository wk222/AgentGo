package officedoc

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// analyze_xlsx treats a worksheet as a table: it finds the header row, reads
// every cell into a typed value (number, text, date, blank) and keeps the
// information a careful analyst would notice - percentages stored as 0.958 or
// as the text "95.8%", numbers typed as text, blanks, subtotal rows. The query
// engine in xlsx_query.go then computes exactly, so the model never has to add
// numbers up by hand.

const (
	maxAnalyzeCells = 400000
	maxHeaderScan   = 30
	maxIssueLabels  = 4
	formulaProbes   = 5000
)

type cellKind uint8

const (
	ckBlank cellKind = iota
	ckNum
	ckText
	ckDate
)

const (
	flFromText uint8 = 1 << iota // a number that the sheet stores as text
	flBad                        // text in a numeric column that is not a number
)

type aCell struct {
	kind cellKind
	flag uint8
	pct  bool      // number is in percentage points (95.8 means 95.8%)
	frac bool      // the sheet stores that percentage as a fraction (0.958)
	n    float64   // number
	s    string    // text, or the displayed text of a date
	t    time.Time // date
}

type aCol struct {
	name     string
	letter   string
	kind     cellKind // ckNum, ckText or ckDate
	unit     string   // "%" for percentage columns
	nonBlank int
}

type aRow struct {
	row   int // 1-based worksheet row
	cells []aCell
}

type aTable struct {
	sheet     string
	headerRow int
	cols      []aCol
	rows      []aRow
	labelCols []int
	notes     []string
}

var (
	dateLikeRes = []*regexp.Regexp{
		regexp.MustCompile(`^\d{4}\s*[-/.年]\s*\d{1,2}(\s*[-/.月]\s*\d{1,2}\s*日?)?\s*月?`),
		regexp.MustCompile(`^\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4}`),
		regexp.MustCompile(`^[A-Za-z]{3,9}[- ]\d{2,4}$`),
	}
	totalWords  = []string{"合计", "总计", "小计", "汇总", "总和", "均值", "平均", "total", "subtotal", "average", "grand"}
	totalExact  = map[string]bool{"全省": true, "全市": true, "全国": true, "全区": true, "全县": true, "全部": true}
	placeholder = map[string]bool{"-": true, "--": true, "—": true, "——": true, "/": true, "n/a": true, "na": true,
		"null": true, "无": true, "暂无": true, "空": true, "#n/a": true, "nan": true}
)

// loadTable reads sheet into a typed table. headerRow is 1-based; 0 detects it.
func loadTable(f *excelize.File, sheet string, headerRow int, keepTotals bool) (*aTable, error) {
	disp, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	raw, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	nRows, nCols := max(len(disp), len(raw)), 0
	for _, g := range [][][]string{disp, raw} {
		for _, r := range g {
			nCols = max(nCols, len(r))
		}
	}
	if dim, _ := f.GetSheetDimension(sheet); strings.Contains(dim, ":") {
		if _, _, c2, r2, e := parseRange(dim); e == nil {
			nCols, nRows = max(nCols, c2), max(nRows, r2)
		}
	}
	if nRows == 0 || nCols == 0 {
		return nil, errors.New("the sheet is empty")
	}
	if nRows*nCols > maxAnalyzeCells {
		return nil, fmt.Errorf("the sheet is too large to analyze at once (%d rows x %d columns)", nRows, nCols)
	}
	dg, rg := padGrid(disp, nRows, nCols), padGrid(raw, nRows, nCols)
	fillMerged(f, sheet, dg, rg)

	if headerRow <= 0 {
		headerRow = detectHeader(dg)
	}
	if headerRow < 1 || headerRow >= nRows {
		return nil, fmt.Errorf("header_row %d is outside the data (the sheet has %d rows)", headerRow, nRows)
	}
	t := &aTable{sheet: sheet, headerRow: headerRow}
	t.cols = headerColumns(dg[headerRow-1])
	probes := formulaProbes
	var skipped []string
	for r := headerRow; r < nRows; r++ {
		line := r + 1
		nonEmpty := 0
		for c := 0; c < nCols; c++ {
			if strings.TrimSpace(dg[r][c]) == "" && strings.TrimSpace(rg[r][c]) == "" && probes > 0 {
				// A formula without a stored result reads as blank: calculate it.
				probes--
				if v, d, ok := calcFormulaCell(f, sheet, c+1, line); ok {
					rg[r][c], dg[r][c] = v, d
				}
			}
			if strings.TrimSpace(dg[r][c]) != "" || strings.TrimSpace(rg[r][c]) != "" {
				nonEmpty++
			}
		}
		if nonEmpty == 0 {
			continue
		}
		label := firstText(dg[r])
		if _, _, _, isNum := looseNumber(label); nonEmpty <= 1 && nCols >= 3 && !isNum { // a lone number is a data row; a lone sentence is a note
			skipped = append(skipped, fmt.Sprintf("row %d only has %q: treated as a note, not data", line, clipRunes(label, 30)))
			continue
		}
		if !keepTotals && isTotalLabel(label) {
			skipped = append(skipped, fmt.Sprintf("row %d %q looks like a total row and is left out (set keep_total_rows to include it)", line, clipRunes(label, 30)))
			continue
		}
		row := aRow{row: line, cells: make([]aCell, len(t.cols))}
		for c := range t.cols {
			row.cells[c] = classifyCell(rg[r][c], dg[r][c])
		}
		t.rows = append(t.rows, row)
	}
	if len(t.rows) == 0 {
		return nil, fmt.Errorf("no data rows below the header (row %d)", headerRow)
	}
	t.notes = append(t.notes, limitStrings(skipped, 8)...)
	t.finalizeColumns()
	return t, nil
}

// calcFormulaCell returns the calculated raw and displayed value of a formula
// cell, or ok=false when the cell holds no formula or it cannot be calculated.
func calcFormulaCell(f *excelize.File, sheet string, col, row int) (raw, disp string, ok bool) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	if form, _ := f.GetCellFormula(sheet, cell); form == "" {
		return "", "", false
	}
	v, err := f.CalcCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil || v == "" {
		return "", "", false
	}
	d, _ := f.CalcCellValue(sheet, cell)
	return v, d, true
}

func padGrid(g [][]string, nRows, nCols int) [][]string {
	out := make([][]string, nRows)
	for r := range out {
		out[r] = make([]string, nCols)
		if r < len(g) {
			copy(out[r], g[r])
		}
	}
	return out
}

// fillMerged copies a merged area's value into all of its cells, so a city name
// merged over several month rows belongs to each of them.
func fillMerged(f *excelize.File, sheet string, dg, rg [][]string) {
	merged, err := f.GetMergeCells(sheet)
	if err != nil {
		return
	}
	for _, m := range merged {
		c1, r1, e1 := excelize.CellNameToCoordinates(m.GetStartAxis())
		c2, r2, e2 := excelize.CellNameToCoordinates(m.GetEndAxis())
		if e1 != nil || e2 != nil || r1 > len(dg) || c1 > len(dg[0]) {
			continue
		}
		d, r := dg[r1-1][c1-1], rg[r1-1][c1-1]
		for y := r1; y <= min(r2, len(dg)); y++ {
			for x := c1; x <= min(c2, len(dg[0])); x++ {
				if y == r1 && x == c1 {
					continue
				}
				dg[y-1][x-1], rg[y-1][x-1] = d, r
			}
		}
	}
}

// detectHeader returns the 1-based header row: the first of the top rows with
// at least two distinct, mostly textual cells and a populated row below it.
// Title rows (one cell, or one merged value repeated) do not qualify.
func detectHeader(dg [][]string) int {
	limit := min(len(dg), maxHeaderScan)
	for r := 0; r < limit && r+1 < len(dg); r++ {
		ne, text, seen := 0, 0, map[string]bool{}
		for _, c := range dg[r] {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			ne++
			seen[c] = true
			if _, _, _, ok := looseNumber(c); !ok {
				text++
			}
		}
		if ne < 2 || len(seen) != ne || text*10 < ne*7 {
			continue
		}
		next := 0
		for _, c := range dg[r+1] {
			if strings.TrimSpace(c) != "" {
				next++
			}
		}
		if next*2 >= ne {
			return r + 1
		}
	}
	for r := 0; r < len(dg)-1; r++ { // no clear header: first non-empty row
		if firstText(dg[r]) != "" {
			return r + 1
		}
	}
	return 1
}

// headerColumns names the columns from the header row; blank and repeated
// names get a letter or a suffix so every column can be addressed.
func headerColumns(header []string) []aCol {
	cols := make([]aCol, len(header))
	seen := map[string]int{}
	for i, h := range header {
		name := strings.Join(strings.Fields(strings.ReplaceAll(h, "\u00a0", " ")), " ")
		letter := colName(i + 1)
		if name == "" {
			name = "col " + letter
		}
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s_%d", name, n)
		}
		cols[i] = aCol{name: name, letter: letter, kind: ckText}
	}
	return cols
}

func firstText(row []string) string {
	for _, c := range row {
		if c = strings.TrimSpace(c); c != "" {
			return c
		}
	}
	return ""
}

func isTotalLabel(label string) bool {
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > 20 {
		return false
	}
	if totalExact[label] {
		return true
	}
	low := strings.ToLower(label)
	for _, w := range totalWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// classifyCell types one cell from its raw value and its displayed text.
func classifyCell(raw, disp string) aCell {
	raw, disp = strings.TrimSpace(raw), strings.TrimSpace(disp)
	if raw == "" && disp == "" {
		return aCell{}
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && !codeLike(raw) {
		d := firstNonEmpty(disp, raw)
		switch {
		case strings.EqualFold(d, "true") || strings.EqualFold(d, "false"):
			return aCell{kind: ckText, s: strings.ToUpper(d)}
		case strings.HasSuffix(d, "%") || strings.HasSuffix(d, "％"):
			// 0.0% shows 95.8% for the stored 0.958; a literal-% format shows it for 95.8.
			if dv, _, _, ok := looseNumber(d); ok && math.Abs(dv-n) < math.Abs(dv-n*100) {
				return aCell{kind: ckNum, pct: true, n: n}
			}
			return aCell{kind: ckNum, pct: true, frac: true, n: roundTo(n*100, 8)}
		case d != raw && isDateText(d) && n > 0 && n < 2958466:
			if tm, e := excelize.ExcelDateToTime(n, false); e == nil {
				return aCell{kind: ckDate, t: tm, s: d}
			}
		}
		return aCell{kind: ckNum, n: n}
	}
	return aCell{kind: ckText, s: firstNonEmpty(disp, raw)}
}

// codeLike reports a digit string that is an identifier, not a quantity:
// "007" or a 12+ digit number such as a phone or ID number.
func codeLike(raw string) bool {
	s := strings.TrimLeft(raw, "+-")
	if len(s) > 1 && s[0] == '0' && s[1] != '.' {
		return true
	}
	if len(s) >= 12 && strings.Trim(s, "0123456789") == "" {
		return true
	}
	return false
}

func isDateText(d string) bool {
	for _, re := range dateLikeRes {
		if re.MatchString(d) {
			return true
		}
	}
	return false
}

// looseNumber parses the number forms people type: 1,234  95.8%  ￥12  ＋3.
// pct is true for a trailing percent sign; signed when a %, thousands comma or
// currency sign was present.
func looseNumber(s string) (v float64, pct, signed, ok bool) {
	s = strings.NewReplacer("\u00a0", "", "\u3000", "", " ", "", "，", ",", "％", "%", "＋", "+", "－", "-", "−", "-", "．", ".").Replace(s)
	for _, p := range []string{"¥", "￥", "$"} {
		if strings.HasPrefix(s, p) {
			s, signed = strings.TrimPrefix(s, p), true
		}
	}
	if strings.HasSuffix(s, "%") {
		s, pct, signed = strings.TrimSuffix(s, "%"), true, true
	}
	if strings.Contains(s, ",") {
		s, signed = strings.ReplaceAll(s, ",", ""), true
	}
	if s == "" || !strings.ContainsRune("+-.0123456789", rune(s[0])) {
		return 0, false, false, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false, false, false
	}
	return n, pct, signed, true
}

func roundTo(x float64, dec int) float64 {
	p := math.Pow(10, float64(dec))
	return math.Round(x*p) / p
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func limitStrings(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("... and %d more", len(s)-n))
}

// finalizeColumns decides each column's type from the majority of its cells
// and normalizes the minority: text numbers become numbers, stray text in a
// numeric column becomes a flagged blank, numbers in a text column become text.
func (t *aTable) finalizeColumns() {
	for c := range t.cols {
		nb, nNum, nDate, nTextNum, nSigned := 0, 0, 0, 0, 0
		pct := false
		for i := range t.rows {
			cell := t.rows[i].cells[c]
			switch cell.kind {
			case ckBlank:
				continue
			case ckNum:
				nNum++
				pct = pct || cell.pct
			case ckDate:
				nDate++
			case ckText:
				if _, p, signed, ok := looseNumber(cell.s); ok {
					nTextNum++
					pct = pct || p
					if signed {
						nSigned++
					}
				}
			}
			nb++
		}
		col := &t.cols[c]
		col.nonBlank = nb
		switch {
		case nb == 0:
			col.kind = ckText
		case nDate*10 >= nb*7:
			col.kind = ckDate
		case (nNum+nTextNum)*10 >= nb*7 && (nNum > 0 || nSigned > 0):
			col.kind = ckNum
			if pct {
				col.unit = "%"
			}
		default:
			col.kind = ckText
		}
		for i := range t.rows {
			cell := &t.rows[i].cells[c]
			switch col.kind {
			case ckNum:
				t.coerceNumber(cell)
			case ckDate:
				// text in a date column stays text
			default:
				if cell.kind == ckNum || cell.kind == ckDate {
					*cell = aCell{kind: ckText, s: aText(*cell)}
				}
			}
		}
	}
	for c, col := range t.cols {
		if col.kind != ckNum && len(t.labelCols) < 2 {
			t.labelCols = append(t.labelCols, c)
		}
	}
}

func (t *aTable) coerceNumber(cell *aCell) {
	switch cell.kind {
	case ckText:
		if placeholder[strings.ToLower(strings.TrimSpace(cell.s))] {
			*cell = aCell{}
			return
		}
		if v, p, _, ok := looseNumber(cell.s); ok {
			*cell = aCell{kind: ckNum, n: v, pct: p, flag: flFromText}
			return
		}
		*cell = aCell{flag: flBad, s: cell.s}
	case ckDate:
		*cell = aCell{flag: flBad, s: cell.s}
	}
}

func aText(c aCell) string {
	switch c.kind {
	case ckNum:
		return formatNumber(c.n)
	case ckDate, ckText:
		return c.s
	}
	return ""
}

func formatNumber(n float64) string {
	if n == math.Trunc(n) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// label names a row for messages: its first one or two text/date cells.
func (t *aTable) label(r aRow) string {
	var parts []string
	for _, c := range t.labelCols {
		if s := aText(r.cells[c]); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "row " + strconv.Itoa(r.row)
	}
	return strings.Join(parts, " ")
}

// cellRef is the A1 address of a table cell.
func (t *aTable) cellRef(r aRow, col int) string {
	return t.cols[col].letter + strconv.Itoa(r.row)
}
