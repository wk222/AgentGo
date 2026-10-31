package officedoc

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxQueries = 12

type analyzeXlsxInput struct {
	RelativePath  string   `json:"relative_path" jsonschema:"required,description=Workspace-relative path of an existing .xlsx workbook."`
	Sheet         string   `json:"sheet,omitempty" jsonschema:"description=Sheet to analyze. Default: the first sheet."`
	HeaderRow     int      `json:"header_row,omitempty" jsonschema:"description=Row number of the column headers. Default: detected (title and note rows above it are skipped)."`
	KeepTotalRows bool     `json:"keep_total_rows,omitempty" jsonschema:"description=Keep rows labelled total / subtotal / average. Default false: they are left out so they are not counted twice."`
	Queries       []aQuery `json:"queries,omitempty" jsonschema:"description=Up to 12 queries answered in one call. Default: one profile of the columns."`
}

type aColInfo struct {
	Name     string   `json:"name"`
	Letter   string   `json:"col"`
	Type     string   `json:"type"`
	Unit     string   `json:"unit,omitempty"`
	Stored   string   `json:"stored,omitempty"`
	NonBlank int      `json:"non_blank"`
	Blank    int      `json:"blank,omitempty"`
	Distinct int      `json:"distinct,omitempty"`
	Values   []string `json:"values,omitempty"`
	Range    string   `json:"range,omitempty"`
	Min      any      `json:"min,omitempty"`
	Max      any      `json:"max,omitempty"`
	Mean     any      `json:"mean,omitempty"`
	Sum      any      `json:"sum,omitempty"`
	Issues   []string `json:"issues,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

type aResult struct {
	Name        string            `json:"name,omitempty"`
	Op          string            `json:"op"`
	Columns     []string          `json:"columns,omitempty"`
	Units       map[string]string `json:"units,omitempty"`
	Rows        [][]any           `json:"rows,omitempty"`
	MatchedRows int               `json:"matched_rows"`
	RowCount    int               `json:"row_count,omitempty"`
	Truncated   bool              `json:"truncated,omitempty"`
	Profile     []aColInfo        `json:"profile,omitempty"`
	Notes       []string          `json:"notes,omitempty"`
	Error       string            `json:"error,omitempty"`
}

type analyzeXlsxOutput struct {
	Path      string     `json:"path"`
	Sheet     string     `json:"sheet"`
	Sheets    []string   `json:"sheets,omitempty"`
	HeaderRow int        `json:"header_row"`
	DataRows  int        `json:"data_rows"`
	Columns   []aColInfo `json:"columns,omitempty"`
	Results   []aResult  `json:"results"`
	Notes     []string   `json:"notes,omitempty"`
}

func analyzeXlsx(env Env, in analyzeXlsxInput) (analyzeXlsxOutput, error) {
	f, rel, err := openWorkbook(env, in.RelativePath)
	if err != nil {
		return analyzeXlsxOutput{}, err
	}
	defer f.Close()
	names := f.GetSheetList()
	if len(names) == 0 {
		return analyzeXlsxOutput{}, fmt.Errorf("the workbook has no sheets")
	}
	sheet, err := pickSheet(names, in.Sheet)
	if err != nil {
		return analyzeXlsxOutput{}, err
	}
	t, err := loadTable(f, sheet, in.HeaderRow, in.KeepTotalRows)
	if err != nil {
		return analyzeXlsxOutput{}, fmt.Errorf("%s: %w", sheet, err)
	}
	queries := in.Queries
	if len(queries) == 0 {
		queries = []aQuery{{Op: "profile"}}
	}
	if len(queries) > maxQueries {
		return analyzeXlsxOutput{}, fmt.Errorf("at most %d queries per call (got %d)", maxQueries, len(queries))
	}
	out := analyzeXlsxOutput{Path: filepath.ToSlash(rel), Sheet: sheet, HeaderRow: t.headerRow, DataRows: len(t.rows), Notes: t.notes}
	if len(names) > 1 {
		out.Sheets = names
	}
	profiled := false
	for _, q := range queries {
		res := t.runQuery(q)
		profiled = profiled || res.Op == "profile"
		out.Results = append(out.Results, res)
	}
	if !profiled {
		out.Columns = t.colInfos(false)
	}
	return out, nil
}

var opAlias = map[string]string{
	"": "aggregate", "aggregate": "aggregate", "agg": "aggregate", "group": "aggregate", "summary": "aggregate",
	"profile": "profile", "compare": "compare", "pivot": "pivot", "rows": "rows", "list": "rows",
}

// runQuery answers one query. A failure is reported in the result so the other
// queries of the call still return.
func (t *aTable) runQuery(q aQuery) aResult {
	op, ok := opAlias[strings.ToLower(strings.TrimSpace(q.Op))]
	res := aResult{Name: q.Name, Op: op}
	if !ok {
		res.Op, res.Error = q.Op, fmt.Sprintf("unknown op %q (use profile, aggregate, compare, pivot or rows)", q.Op)
		return res
	}
	if op == "profile" {
		res.Profile = t.colInfos(true)
		return res
	}
	rows, notes, err := t.filter(q.Where)
	res.MatchedRows, res.Notes = len(rows), notes
	if err != nil {
		res.Error = err.Error()
		return res
	}
	var (
		rt   *rtab
		used = rows
		def  = defaultResultRows
	)
	switch op {
	case "aggregate":
		rt, err = t.aggregate(rows, q)
	case "compare":
		var more []string
		rt, used, more, err = t.compare(rows, q)
		res.Notes = append(res.Notes, more...)
	case "pivot":
		rt, err = t.pivot(rows, q)
	case "rows":
		rt, err = t.listRows(rows, q)
		def = 30
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if op != "rows" {
		res.Notes = append(res.Notes, t.metricNotes(used, t.metricCols(q))...)
	}
	matched, truncated, tie, err := rt.finish(q, def)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	for _, c := range rt.cols {
		res.Columns = append(res.Columns, c.name)
	}
	res.Units, res.Rows, res.RowCount, res.Truncated = rt.units(), rt.render(q.Decimals), matched, truncated
	if tie != "" {
		res.Notes = append(res.Notes, tie)
	}
	if truncated && q.Limit <= 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("showing %d of %d rows; narrow with where/having or raise limit (max %d)", len(res.Rows), matched, maxResultRows))
	}
	return res
}

// metricCols lists the source columns a query computes on.
func (t *aTable) metricCols(q aQuery) []int {
	var cols []int
	for _, m := range q.Metrics {
		for _, name := range []string{m.Col, m.Weight} {
			if strings.TrimSpace(name) == "" {
				continue
			}
			if ci, err := t.colIndex(name); err == nil {
				cols = append(cols, ci)
			}
		}
	}
	return cols
}

func typeName(c aCol) string {
	switch c.kind {
	case ckNum:
		if c.unit == "%" {
			return "percent"
		}
		return "number"
	case ckDate:
		return "date"
	}
	return "text"
}

// colInfos describes the columns; detailed adds statistics, value lists and
// data-quality findings.
func (t *aTable) colInfos(detailed bool) []aColInfo {
	var out []aColInfo
	for ci, c := range t.cols {
		if c.nonBlank == 0 && strings.HasPrefix(c.name, "col ") {
			continue
		}
		info := aColInfo{Name: c.name, Letter: c.letter, Type: typeName(c), Unit: c.unit, Stored: t.stored(ci), NonBlank: c.nonBlank, Blank: len(t.rows) - c.nonBlank}
		if c.kind == ckNum {
			info.Issues = t.colIssues(ci)
		}
		if detailed {
			t.fillStats(ci, &info)
		}
		out = append(out, info)
	}
	return out
}

func (t *aTable) fillStats(ci int, info *aColInfo) {
	c := t.cols[ci]
	switch c.kind {
	case ckNum:
		first := true
		var mn, mx, sum float64
		n := 0
		for _, r := range t.rows {
			v := r.cells[ci]
			if v.kind != ckNum {
				continue
			}
			if first || v.n < mn {
				mn = v.n
			}
			if first || v.n > mx {
				mx = v.n
			}
			first = false
			sum += v.n
			n++
		}
		if n > 0 {
			info.Min, info.Max = number(roundTo(mn, 4)), number(roundTo(mx, 4))
			info.Mean, info.Sum = number(roundTo(sum/float64(n), 4)), number(roundTo(sum, 4))
			low := strings.ToLower(c.name)
			if c.unit == "" && mn >= 0 && mx <= 1 && n > 3 &&
				(strings.Contains(low, "率") || strings.Contains(low, "占比") || strings.Contains(low, "比例") || strings.Contains(low, "%")) {
				info.Hint = "all values are between 0 and 1: a share stored as a fraction (0.95 = 95%); multiply by 100 when you write it as a percentage"
			}
		}
	default:
		seen := map[string]bool{}
		var vals []string
		var lo, hi aCell
		for _, r := range t.rows {
			v := r.cells[ci]
			if v.kind == ckBlank {
				continue
			}
			s := aText(v)
			if !seen[s] {
				seen[s] = true
				vals = append(vals, s)
			}
			if v.kind == ckDate {
				if lo.kind == ckBlank || v.t.Before(lo.t) {
					lo = v
				}
				if hi.kind == ckBlank || v.t.After(hi.t) {
					hi = v
				}
			}
		}
		info.Distinct = len(vals)
		if len(vals) <= 30 {
			info.Values = vals
		} else {
			info.Values = vals[:5]
		}
		if lo.kind == ckDate {
			info.Range = lo.s + " ~ " + hi.s
		}
	}
}

// stored says how the sheet really holds a percentage or date column. The cells
// read back as display text ("95.8%", "2026-09"), so a formula written from that
// text compares the wrong thing: the percentage is the number 0.958 and the
// month is a date, not the string "2026-09".
func (t *aTable) stored(ci int) string {
	c := t.cols[ci]
	nReal, nFrac, nDate, nTextCell := 0, 0, 0, 0
	for _, r := range t.rows {
		cell := r.cells[ci]
		switch {
		case cell.kind == ckDate:
			nDate++
		case cell.kind == ckNum && cell.flag&flFromText != 0:
		case cell.kind == ckNum:
			nReal++
			if cell.frac {
				nFrac++
			}
		case cell.kind == ckText && c.kind == ckDate:
			nTextCell++
		}
	}
	var s string
	switch {
	case c.kind == ckDate && nDate > 0:
		s = "real dates (not text): match with TEXT(cell,\"yyyy-mm\")=\"2026-09\" or MONTH()/YEAR(), never cell=\"2026-09\""
		if nTextCell > 0 {
			s += fmt.Sprintf("; %d cell(s) are text instead", nTextCell)
		}
	case c.kind == ckNum && c.unit == "%" && nFrac > 0 && nFrac*2 >= nReal:
		s = "numbers stored as fractions (0.958 shows as 95.8%): compare with 0.96 not 96, and (cell1-cell2)*100 gives percentage points"
	case c.kind == ckNum && c.unit == "%" && nReal > 0:
		s = "numbers stored as percentage points with a literal % format (95.8 shows as 95.8%)"
	}
	return s
}

// colIssues lists the cells of a numeric column that need a human's attention.
func (t *aTable) colIssues(ci int) []string {
	var blanks, texts, bads []string
	nb, nt, nbad := 0, 0, 0
	for _, r := range t.rows {
		c := r.cells[ci]
		ref := fmt.Sprintf("%s (%s)", t.cellRef(r, ci), t.label(r))
		switch {
		case c.flag&flBad != 0:
			nbad++
			bads = append(bads, fmt.Sprintf("%s %q", ref, clipRunes(c.s, 20)))
		case c.kind == ckBlank:
			nb++
			blanks = append(blanks, ref)
		case c.flag&flFromText != 0:
			nt++
			texts = append(texts, ref)
		}
	}
	var out []string
	if nb > 0 {
		out = append(out, fmt.Sprintf("%d blank cell(s): %s", nb, strings.Join(limitStrings(blanks, maxIssueLabels), ", ")))
	}
	if nt > 0 {
		out = append(out, fmt.Sprintf("%d number(s) stored as text, read as numbers here (convert them in the file with edit_xlsx to_number): %s", nt, strings.Join(limitStrings(texts, maxIssueLabels), ", ")))
	}
	if nbad > 0 {
		out = append(out, fmt.Sprintf("%d cell(s) with text that is not a number, ignored: %s", nbad, strings.Join(limitStrings(bads, maxIssueLabels), ", ")))
	}
	return out
}
