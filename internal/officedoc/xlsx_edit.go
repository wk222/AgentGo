package officedoc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// Limits for one edit_xlsx call.
const (
	maxXlsxOps      = 300
	maxOpCells      = 20000
	maxFormatCells  = 50000
	maxEditSheetLen = 31
)

type editXlsxInput struct {
	RelativePath string   `json:"relative_path" jsonschema:"required,description=Workspace-relative path of the existing .xlsx to modify."`
	Operations   []xlsxOp `json:"operations" jsonschema:"required,description=Edits applied in order on one open workbook. If any fails nothing is saved."`
	SaveAs       string   `json:"save_as,omitempty" jsonschema:"description=Write the result to this new path and leave the original untouched. Fails if that file exists unless overwrite is true."`
	Overwrite    bool     `json:"overwrite,omitempty" jsonschema:"description=With save_as: replace that file if it exists."`
	NoBackup     bool     `json:"no_backup,omitempty" jsonschema:"description=Skip the backup copy that is made before an in-place edit."`
}

// xlsxOp is one edit. Which fields matter depends on op.
type xlsxOp struct {
	Op      string      `json:"op" jsonschema:"required,description=set_cell | set_range | clear_range | add_sheet | rename_sheet | delete_sheet | insert_rows | delete_rows | format | col_width | merge | freeze | filter | add_chart | delete_chart | fill_formula | conditional_format | to_number | copy_format"`
	Sheet   string      `json:"sheet,omitempty" jsonschema:"description=Sheet to act on (default: the first sheet). For rename_sheet and delete_sheet: the existing sheet."`
	Cell    string      `json:"cell,omitempty" jsonschema:"description=A cell such as B2: the cell for set_cell\\, the top-left cell for set_range\\, the anchor for add_chart and delete_chart (the anchor read_xlsx reports)\\, the source cell for copy_format\\, the first scrolling cell for freeze (A2 freezes row 1\\; B2 row 1 and column A\\; A1 unfreezes)."`
	Range   string      `json:"range,omitempty" jsonschema:"description=A range such as A1:C9 for clear_range\\, format\\, merge\\, filter\\, fill_formula\\, conditional_format\\, to_number and copy_format (the target)."`
	Formula string      `json:"formula,omitempty" jsonschema:"description=fill_formula: the formula for the TOP-LEFT cell of range\\, e.g. =IF(A5=A4\\,C5-C4\\,\"\")\\; it is copied to every cell of range with relative references adjusted as in Excel (use $ to anchor)."`
	Value   any         `json:"value,omitempty" jsonschema:"description=set_cell value: a number\\, boolean or string\\; a string starting with = is a formula\\; null clears the cell."`
	Rows    [][]any     `json:"rows,omitempty" jsonschema:"description=set_range values\\, row by row (same rules as value)."`
	Name    string      `json:"name,omitempty" jsonschema:"description=New sheet name for add_sheet and rename_sheet."`
	Row     int         `json:"row,omitempty" jsonschema:"description=insert_rows: insert before this row number (the new rows copy the cell formats of the row above or of the row below when inserting at row 2)\\; delete_rows: first row to delete."`
	Count   int         `json:"count,omitempty" jsonschema:"description=insert_rows / delete_rows: how many rows (default 1)."`
	Columns string      `json:"columns,omitempty" jsonschema:"description=col_width: columns such as A or A:C."`
	Width   float64     `json:"width,omitempty" jsonschema:"description=col_width: width in characters (about 8-60)."`
	Format  *xlsxFormat `json:"format,omitempty" jsonschema:"description=format: what to apply to range\\; other cell styling is kept. conditional_format: the fill / font_color / bold the matching cells get."`
	Rule    *xlsxRule   `json:"rule,omitempty" jsonschema:"description=conditional_format: which cells of range to colour."`
	Chart   *xlsxChart  `json:"chart,omitempty" jsonschema:"description=add_chart: the chart to place at cell."`
}

type xlsxFormat struct {
	Bold      *bool   `json:"bold,omitempty"`
	Italic    *bool   `json:"italic,omitempty"`
	FontSize  float64 `json:"font_size,omitempty"`
	FontColor string  `json:"font_color,omitempty" jsonschema:"description=#RRGGBB"`
	Fill      string  `json:"fill,omitempty" jsonschema:"description=Background colour #RRGGBB."`
	NumFmt    string  `json:"num_fmt,omitempty" jsonschema:"description=Number format such as 0.00\\, #\\,##0\\, 0.0%\\, yyyy-mm-dd."`
	Align     string  `json:"align,omitempty" jsonschema:"description=left\\, center or right."`
	Wrap      *bool   `json:"wrap,omitempty"`
	Border    *bool   `json:"border,omitempty" jsonschema:"description=true: thin grey border around every cell\\; false: remove borders."`
}

type xlsxChart struct {
	Type      string `json:"type" jsonschema:"required,description=column\\, stacked_column\\, bar\\, line\\, area\\, pie\\, doughnut or scatter."`
	DataRange string `json:"data_range" jsonschema:"required,description=Range with a header row and a first column of categories\\, e.g. A1:D7 or Data!A1:D7. Every column after the first becomes one series named by its header."`
	Title     string `json:"title,omitempty"`
}

type editXlsxOutput struct {
	createOutput
	Backup string `json:"backup,omitempty"`
}

var hexColorRe = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)

func editXlsx(env Env, in editXlsxInput) (editXlsxOutput, error) {
	if len(in.Operations) == 0 {
		return editXlsxOutput{}, errors.New("operations is required")
	}
	if len(in.Operations) > maxXlsxOps {
		return editXlsxOutput{}, fmt.Errorf("too many operations: %d (max %d)", len(in.Operations), maxXlsxOps)
	}
	f, rel, err := openWorkbook(env, in.RelativePath)
	if err != nil {
		return editXlsxOutput{}, err
	}
	defer f.Close()
	srcAbs, _ := env.Resolve(rel)

	dstRel, inPlace := rel, true
	if s := strings.TrimSpace(in.SaveAs); s != "" {
		dstRel, inPlace = withExt(s, ".xlsx"), false
	}
	dstAbs, err := env.Resolve(dstRel)
	if err != nil {
		return editXlsxOutput{}, err
	}

	touched := map[string]bool{}
	for i, op := range in.Operations {
		if err := applyXlsxOp(f, op, touched); err != nil {
			return editXlsxOutput{}, fmt.Errorf("operations[%d] (%s): %w", i, op.Op, err)
		}
	}
	for sheet := range touched {
		if idx, _ := f.GetSheetIndex(sheet); idx < 0 {
			continue // the sheet was deleted or renamed later in the list
		}
		if cols, rows, err := usedExtent(f, sheet); err == nil && cols > 0 && rows > 0 {
			corner, _ := excelize.CoordinatesToCellName(cols, rows)
			_ = f.SetSheetDimension(sheet, "A1:"+corner)
		}
	}

	data, err := finishWorkbook(f)
	if err != nil {
		return editXlsxOutput{}, err
	}
	out := editXlsxOutput{}
	if inPlace && !in.NoBackup {
		if backup, err := backupFile(srcAbs); err == nil {
			out.Backup = filepath.ToSlash(filepath.Join(filepath.Dir(rel), filepath.Base(backup)))
		}
	}
	if err := writeNew(dstAbs, data, inPlace || in.Overwrite); err != nil {
		return editXlsxOutput{}, err
	}
	out.createOutput = createOutput{
		Path: filepath.ToSlash(dstRel), Bytes: len(data),
		Detail: fmt.Sprintf("%d operations applied", len(in.Operations)),
	}
	return out, nil
}

// backupFile copies path to <stem>.bak-<timestamp><ext> next to it.
func backupFile(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(abs)
	name := strings.TrimSuffix(abs, ext) + ".bak-" + time.Now().Format("20060102-150405") + ext
	return name, writeNew(name, data, false)
}

func applyXlsxOp(f *excelize.File, op xlsxOp, touched map[string]bool) error {
	names := f.GetSheetList()
	kind := strings.ToLower(strings.TrimSpace(op.Op))
	switch kind {
	case "add_sheet":
		return addSheet(f, names, op.Name, touched)
	case "rename_sheet":
		old, err := pickSheet(names, op.Sheet)
		if err != nil || strings.TrimSpace(op.Sheet) == "" {
			return fmt.Errorf("sheet must name an existing sheet (%s)", strings.Join(names, ", "))
		}
		if err := checkSheetName(names, op.Name, old); err != nil {
			return err
		}
		touched[strings.TrimSpace(op.Name)] = true
		return f.SetSheetName(old, strings.TrimSpace(op.Name))
	case "delete_sheet":
		old, err := pickSheet(names, op.Sheet)
		if err != nil || strings.TrimSpace(op.Sheet) == "" {
			return fmt.Errorf("sheet must name an existing sheet (%s)", strings.Join(names, ", "))
		}
		if len(names) == 1 {
			return errors.New("a workbook needs at least one sheet")
		}
		return f.DeleteSheet(old)
	}

	sheet, err := pickSheet(names, op.Sheet)
	if err != nil {
		return err
	}
	switch kind {
	case "set_cell":
		if err := cellArg(op.Cell); err != nil {
			return err
		}
		touched[sheet] = true
		return setCell(f, sheet, op.Cell, op.Value)
	case "set_range":
		touched[sheet] = true
		return setRange(f, sheet, op.Cell, op.Rows)
	case "clear_range":
		touched[sheet] = true
		return eachCell(f, sheet, op.Range, maxOpCells, func(cell string) error { return setCell(f, sheet, cell, nil) })
	case "insert_rows":
		touched[sheet] = true
		return shiftRows(f, sheet, op, true)
	case "delete_rows":
		touched[sheet] = true
		return shiftRows(f, sheet, op, false)
	case "format":
		if op.Format == nil {
			return errors.New("format is required")
		}
		return applyFormat(f, sheet, op.Range, op.Format)
	case "col_width":
		if op.Width <= 0 || op.Width > 255 {
			return errors.New("width must be between 1 and 255")
		}
		c1, c2, err := columnSpan(op.Columns)
		if err != nil {
			return err
		}
		return f.SetColWidth(sheet, colName(c1), colName(c2), op.Width)
	case "merge":
		c1, r1, c2, r2, err := parseRange(op.Range)
		if err != nil {
			return fmt.Errorf("range %q: %w", op.Range, err)
		}
		tl, _ := excelize.CoordinatesToCellName(c1, r1)
		br, _ := excelize.CoordinatesToCellName(c2, r2)
		return f.MergeCell(sheet, tl, br)
	case "freeze":
		return freezeAt(f, sheet, op.Cell)
	case "filter":
		if _, _, _, _, err := parseRange(op.Range); err != nil {
			return fmt.Errorf("range %q: %w", op.Range, err)
		}
		return f.AutoFilter(sheet, strings.ReplaceAll(op.Range, "$", ""), nil)
	case "add_chart":
		return addChart(f, sheet, op)
	case "delete_chart":
		return deleteChartAt(f, sheet, op.Cell)
	case "fill_formula":
		touched[sheet] = true
		return fillFormula(f, sheet, op.Range, op.Formula)
	case "conditional_format":
		return conditionalFormat(f, sheet, op.Range, op.Rule, op.Format)
	case "copy_format":
		return copyFormat(f, sheet, op.Cell, op.Range)
	case "to_number":
		touched[sheet] = true
		_, err := toNumber(f, sheet, op.Range)
		return err
	}
	return fmt.Errorf("unknown op %q", op.Op)
}

func cellArg(cell string) error {
	if strings.TrimSpace(cell) == "" {
		return errors.New("cell is required")
	}
	if _, _, err := excelize.CellNameToCoordinates(strings.ReplaceAll(strings.TrimSpace(cell), "$", "")); err != nil {
		return fmt.Errorf("cell %q: %w", cell, err)
	}
	return nil
}

// setCell stores one value: numbers and booleans typed, text as text, a leading
// "=" as a formula, nil as an empty cell.
func setCell(f *excelize.File, sheet, cell string, v any) error {
	cell = strings.ReplaceAll(strings.TrimSpace(cell), "$", "")
	if v == nil {
		if err := f.SetCellFormula(sheet, cell, ""); err != nil {
			return err
		}
		return f.SetCellValue(sheet, cell, nil)
	}
	if s, ok := v.(string); ok && strings.HasPrefix(s, "=") && len(s) > 1 {
		return f.SetCellFormula(sheet, cell, strings.TrimPrefix(s, "="))
	}
	return f.SetCellValue(sheet, cell, cleanCell(v))
}

func setRange(f *excelize.File, sheet, start string, rows [][]any) error {
	if err := cellArg(start); err != nil {
		return err
	}
	if len(rows) == 0 {
		return errors.New("rows is required")
	}
	c0, r0, _ := excelize.CellNameToCoordinates(strings.ReplaceAll(strings.TrimSpace(start), "$", ""))
	total := 0
	for ri, row := range rows {
		total += len(row)
		if total > maxOpCells {
			return fmt.Errorf("too many cells in one set_range (max %d)", maxOpCells)
		}
		for ci, v := range row {
			cell, err := excelize.CoordinatesToCellName(c0+ci, r0+ri)
			if err != nil {
				return err
			}
			if err := setCell(f, sheet, cell, v); err != nil {
				return fmt.Errorf("%s: %w", cell, err)
			}
		}
	}
	return nil
}

func eachCell(f *excelize.File, sheet, rng string, limit int, fn func(cell string) error) error {
	c1, r1, c2, r2, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("range %q: %w", rng, err)
	}
	if (c2-c1+1)*(r2-r1+1) > limit {
		return fmt.Errorf("range %q is too large (max %d cells)", rng, limit)
	}
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			cell, _ := excelize.CoordinatesToCellName(c, r)
			if err := fn(cell); err != nil {
				return err
			}
		}
	}
	return nil
}

func shiftRows(f *excelize.File, sheet string, op xlsxOp, insert bool) error {
	if op.Row < 1 {
		return errors.New("row must be 1 or greater")
	}
	n := max(op.Count, 1)
	if n > 1000 {
		return errors.New("count must be at most 1000")
	}
	if insert {
		if err := f.InsertRows(sheet, op.Row, n); err != nil {
			return err
		}
		inheritRowStyle(f, sheet, op.Row, n)
		return nil
	}
	for i := 0; i < n; i++ {
		if err := f.RemoveRow(sheet, op.Row); err != nil {
			return err
		}
	}
	return nil
}

// inheritRowStyle gives n freshly inserted rows the cell formats of a neighbouring
// row, the way Excel does, so a new line keeps its number format and borders
// instead of coming out unlike the rows around it. The row above is the model;
// directly under the first row (nearly always a header) it is the row below, and
// above the first row there is nothing to copy. Best effort: a failure here only
// leaves the new row unformatted.
func inheritRowStyle(f *excelize.File, sheet string, row, n int) {
	src := row - 1
	switch {
	case row <= 1:
		return
	case row == 2:
		src = row + n // the old row 2, pushed down by the insert
	}
	dim, err := f.GetSheetDimension(sheet)
	if err != nil || dim == "" {
		return
	}
	_, _, cols, _, err := parseRange(dim)
	if err != nil || cols < 1 || cols*n > maxOpCells {
		return
	}
	for c := 1; c <= cols; c++ {
		from, err := excelize.CoordinatesToCellName(c, src)
		if err != nil {
			return
		}
		id, err := f.GetCellStyle(sheet, from)
		if err != nil || id == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			to, err := excelize.CoordinatesToCellName(c, row+i)
			if err != nil {
				return
			}
			_ = f.SetCellStyle(sheet, to, to, id)
		}
	}
}

func columnSpan(s string) (c1, c2 int, err error) {
	a, b, found := strings.Cut(strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(s)), "$", ""), ":")
	if !found {
		b = a
	}
	if c1, err = excelize.ColumnNameToNumber(strings.TrimSpace(a)); err != nil {
		return 0, 0, fmt.Errorf("columns %q: %w", s, err)
	}
	if c2, err = excelize.ColumnNameToNumber(strings.TrimSpace(b)); err != nil {
		return 0, 0, fmt.Errorf("columns %q: %w", s, err)
	}
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	return c1, c2, nil
}

func freezeAt(f *excelize.File, sheet, cell string) error {
	if err := cellArg(cell); err != nil {
		return err
	}
	c, r, _ := excelize.CellNameToCoordinates(strings.ReplaceAll(strings.TrimSpace(cell), "$", ""))
	if c == 1 && r == 1 {
		return f.SetPanes(sheet, &excelize.Panes{})
	}
	p := &excelize.Panes{Freeze: true, XSplit: c - 1, YSplit: r - 1, TopLeftCell: strings.TrimSpace(cell)}
	switch {
	case c > 1 && r > 1:
		p.ActivePane = "bottomRight"
	case r > 1:
		p.ActivePane = "bottomLeft"
	default:
		p.ActivePane = "topRight"
	}
	p.Selection = []excelize.Selection{{SQRef: p.TopLeftCell, ActiveCell: p.TopLeftCell, Pane: p.ActivePane}}
	return f.SetPanes(sheet, p)
}

func addSheet(f *excelize.File, names []string, name string, touched map[string]bool) error {
	if err := checkSheetName(names, name, ""); err != nil {
		return err
	}
	touched[strings.TrimSpace(name)] = true
	_, err := f.NewSheet(strings.TrimSpace(name))
	return err
}

// checkSheetName validates a new sheet name; except is the sheet being renamed.
func checkSheetName(names []string, name, except string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("name is required")
	case len([]rune(name)) > maxEditSheetLen:
		return fmt.Errorf("sheet name %q is longer than %d characters", name, maxEditSheetLen)
	case strings.ContainsAny(name, `[]:*?/\`):
		return fmt.Errorf("sheet name %q must not contain any of []:*?/\\", name)
	case strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'"):
		return errors.New("sheet name must not start or end with an apostrophe")
	}
	for _, n := range names {
		if strings.EqualFold(n, name) && !strings.EqualFold(n, except) {
			return fmt.Errorf("a sheet named %q already exists", n)
		}
	}
	return nil
}

func normalizeColor(c string) (string, error) {
	c = strings.TrimSpace(c)
	if !hexColorRe.MatchString(c) {
		return "", fmt.Errorf("colour %q must look like #RRGGBB", c)
	}
	return "#" + strings.ToUpper(strings.TrimPrefix(c, "#")), nil
}
