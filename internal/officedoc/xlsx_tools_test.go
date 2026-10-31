package officedoc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

func newXlsxTools(t *testing.T) (string, []tool.BaseTool) {
	t.Helper()
	root := t.TempDir()
	ts, err := Tools(Env{Resolve: jailTo(root)})
	if err != nil {
		t.Fatal(err)
	}
	return root, ts
}

// seedBook writes a small budget workbook through the create tool.
func seedBook(t *testing.T, ts []tool.BaseTool, rel string) {
	t.Helper()
	_, err := run(t, ToolCreateXlsx, ts, map[string]any{
		"relative_path": rel,
		"sheets": []map[string]any{
			{"name": "预算", "rows": [][]any{
				{"项目", "金额", "占比"},
				{"人员", 600, "=B2/B4"},
				{"设备", 400, "=B3/B4"},
				{"合计", "=SUM(B2:B3)", "=SUM(C2:C3)"},
			}},
			{"name": "备注", "rows": [][]any{{"说明"}, {"含税"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestXlsxStoresFormulaResultsAndRecalcFlag(t *testing.T) {
	var buf bytes.Buffer
	err := WriteXlsx(&buf, []Sheet{{Name: "S", Header: true, Rows: [][]any{
		{"a", "b"},
		{2, 3},
		{"=A2+B2", `=A1&"!"`},
		{"=A2>1", "=1/0"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for cell, want := range map[string]string{"A3": "5", "B3": "a!", "A4": "TRUE", "B4": "#DIV/0!"} {
		if got, _ := f.GetCellValue("S", cell); got != want {
			t.Errorf("stored value of %s = %q, want %q", cell, got, want)
		}
	}
	if form, _ := f.GetCellFormula("S", "A3"); form != "A2+B2" {
		t.Errorf("formula lost: %q", form)
	}
	if wb := readZip(t, buf.Bytes())["xl/workbook.xml"]; !strings.Contains(wb, `fullCalcOnLoad="true"`) && !strings.Contains(wb, `fullCalcOnLoad="1"`) {
		t.Errorf("workbook must recalculate on open: %s", wb)
	}
	if dim, _ := f.GetSheetDimension("S"); dim != "A1:B4" {
		t.Errorf("dimension = %q, want A1:B4", dim)
	}
}

func TestReadXlsx(t *testing.T) {
	_, ts := newXlsxTools(t)
	seedBook(t, ts, "data/budget.xlsx")

	out, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.xlsx", "formulas": true})
	if err != nil {
		t.Fatal(err)
	}
	var got readXlsxOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sheets) != 2 || got.Sheets[0].Name != "预算" || got.Sheet != "预算" || got.Range != "A1:C4" {
		t.Fatalf("sheets/range = %+v / %q / %q", got.Sheets, got.Sheet, got.Range)
	}
	if len(got.Rows) != 4 || got.Rows[1][0] != "人员" || got.Rows[1][1] != float64(600) {
		t.Fatalf("rows = %v", got.Rows)
	}
	if got.Rows[3][1] != float64(1000) {
		t.Errorf("SUM must be calculated: %v", got.Rows[3])
	}
	if got.Rows[1][2] != 0.6 {
		t.Errorf("ratio = %v, want 0.6", got.Rows[1][2])
	}
	if len(got.Formulas) != 4 || got.Formulas[0].Cell != "C2" || got.Formulas[0].Formula != "=B2/B4" {
		t.Errorf("formulas = %+v", got.Formulas)
	}

	// A named sheet (any letter case) and a sub-range.
	out, err = run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.xlsx", "sheet": "预算", "range": "B2:B3"})
	if err != nil {
		t.Fatal(err)
	}
	got = readXlsxOutput{}
	_ = json.Unmarshal([]byte(out), &got)
	if got.Range != "B2:B3" || len(got.Rows) != 2 || got.Rows[1][0] != float64(400) {
		t.Errorf("sub-range = %+v", got)
	}

	// Paging: max_rows cuts and says so.
	out, _ = run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.xlsx", "max_rows": 2})
	got = readXlsxOutput{}
	_ = json.Unmarshal([]byte(out), &got)
	if !got.Truncated || len(got.Rows) != 2 || len(got.Notes) == 0 {
		t.Errorf("truncation not reported: %+v", got)
	}

	// Search across sheets.
	out, _ = run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.xlsx", "find": "含税"})
	got = readXlsxOutput{}
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Matches) != 1 || got.Matches[0].Sheet != "备注" || got.Matches[0].Cell != "A2" {
		t.Errorf("matches = %+v", got.Matches)
	}

	// Errors are explained to the model.
	if _, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.xlsx", "sheet": "nope"}); err == nil ||
		!strings.Contains(err.Error(), "预算") {
		t.Errorf("unknown sheet must list the real ones: %v", err)
	}
	if _, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "data/budget.csv"}); err == nil {
		t.Error("only .xlsx is accepted")
	}
	if _, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "missing.xlsx"}); err == nil {
		t.Error("a missing file must fail")
	}
	if _, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "../x.xlsx"}); err == nil {
		t.Error("path escape must fail")
	}
}

func TestReadXlsxShowsDisplayTextForFormats(t *testing.T) {
	root, ts := newXlsxTools(t)
	f := excelize.NewFile()
	pct, _ := f.NewStyle(&excelize.Style{NumFmt: 10}) // 0.00%
	_ = f.SetCellValue("Sheet1", "A1", 0.125)
	_ = f.SetCellStyle("Sheet1", "A1", "A1", pct)
	_ = f.SetCellValue("Sheet1", "A2", true)
	_ = f.SetCellValue("Sheet1", "A3", "00123")
	if err := f.SaveAs(filepath.Join(root, "fmt.xlsx")); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": "fmt.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	var got readXlsxOutput
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Rows) != 3 {
		t.Fatalf("rows = %v", got.Rows)
	}
	if got.Rows[0][0] != "12.50%" || got.Rows[1][0] != true || got.Rows[2][0] != "00123" {
		t.Errorf("display values = %v", got.Rows)
	}
}

func readBook(t *testing.T, root, rel string) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestEditXlsxAppliesOperationsAndKeepsABackup(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "budget.xlsx")
	before, _ := os.ReadFile(filepath.Join(root, "budget.xlsx"))

	out, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "budget.xlsx",
		"operations": []map[string]any{
			{"op": "set_cell", "sheet": "预算", "cell": "A4", "value": "总计"},
			{"op": "set_range", "sheet": "预算", "cell": "A5", "rows": [][]any{{"其他", 50, "=B5/B4"}, {"备用", 25, nil}}},
			{"op": "set_cell", "sheet": "预算", "cell": "B4", "value": "=SUM(B2:B3)"},
			{"op": "format", "sheet": "预算", "range": "B2:B6", "format": map[string]any{"num_fmt": "#,##0.00", "fill": "#FFF2CC"}},
			{"op": "format", "sheet": "预算", "range": "A1:C1", "format": map[string]any{"font_color": "#C00000"}},
			{"op": "col_width", "sheet": "预算", "columns": "A:B", "width": 18},
			{"op": "merge", "sheet": "备注", "range": "A1:B1"},
			{"op": "freeze", "sheet": "预算", "cell": "A2"},
			{"op": "filter", "sheet": "预算", "range": "A1:C6"},
			{"op": "add_chart", "sheet": "预算", "cell": "E2",
				"chart": map[string]any{"type": "column", "data_range": "A1:B3", "title": "预算构成"}},
			{"op": "add_sheet", "name": "汇总"},
			{"op": "rename_sheet", "sheet": "备注", "name": "说明"},
			{"op": "insert_rows", "sheet": "说明", "row": 2, "count": 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Path   string `json:"path"`
		Backup string `json:"backup"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal([]byte(out), &res)
	if res.Path != "budget.xlsx" || !strings.HasPrefix(res.Backup, "budget.bak-") || !strings.Contains(res.Detail, "13") {
		t.Fatalf("output = %s", out)
	}
	if bak, err := os.ReadFile(filepath.Join(root, res.Backup)); err != nil || !bytes.Equal(bak, before) {
		t.Errorf("backup must hold the original bytes: %v", err)
	}

	f := readBook(t, root, "budget.xlsx")
	if got := f.GetSheetList(); strings.Join(got, ",") != "预算,说明,汇总" {
		t.Errorf("sheets = %v", got)
	}
	if v, _ := f.GetCellValue("预算", "A4"); v != "总计" {
		t.Errorf("A4 = %q", v)
	}
	if v, _ := f.GetCellValue("预算", "A6"); v != "备用" {
		t.Errorf("A6 = %q", v)
	}
	if form, _ := f.GetCellFormula("预算", "C5"); form != "B5/B4" {
		t.Errorf("C5 formula = %q", form)
	}
	if v, _ := f.GetCellValue("预算", "B4"); v != "1,000.00" {
		t.Errorf("B4 shown as %q, want 1,000.00 (formula result with number format)", v)
	}
	if v, _ := f.GetCellValue("预算", "B2"); v != "600.00" {
		t.Errorf("B2 shown as %q", v)
	}
	// format merges: the header keeps its bold from create_xlsx and gains the colour.
	id, _ := f.GetCellStyle("预算", "A1")
	st, err := f.GetStyle(id)
	if err != nil || st.Font == nil || !st.Font.Bold || !strings.EqualFold(st.Font.Color, "C00000") {
		t.Errorf("header style = %+v err=%v", st, err)
	}
	if merged, _ := f.GetMergeCells("说明"); len(merged) != 1 {
		t.Errorf("merge lost: %v", merged)
	}
	if v, _ := f.GetCellValue("说明", "A4"); v != "含税" {
		t.Errorf("rows were not shifted: A4 = %q", v)
	}
	zipped := readZip(t, mustRead(t, filepath.Join(root, "budget.xlsx")))
	var chart bool
	for name := range zipped {
		if strings.HasPrefix(name, "xl/charts/chart") {
			chart = true
		}
	}
	if !chart {
		t.Error("no chart part written")
	}
	if dim, _ := f.GetSheetDimension("预算"); dim != "A1:C6" {
		t.Errorf("dimension = %q", dim)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEditXlsxIsAllOrNothing(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "b.xlsx")
	before := mustRead(t, filepath.Join(root, "b.xlsx"))

	_, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "b.xlsx",
		"operations": []map[string]any{
			{"op": "set_cell", "sheet": "预算", "cell": "A1", "value": "changed"},
			{"op": "set_cell", "sheet": "没有", "cell": "A1", "value": "x"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "operations[1]") || !strings.Contains(err.Error(), "预算") {
		t.Fatalf("error must name the failing operation and the real sheets: %v", err)
	}
	if after := mustRead(t, filepath.Join(root, "b.xlsx")); !bytes.Equal(after, before) {
		t.Error("a failed edit changed the file")
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") || strings.HasPrefix(e.Name(), ".officedoc-") {
			t.Errorf("a failed edit left %s behind", e.Name())
		}
	}

	for name, op := range map[string]map[string]any{
		"unknown op":       {"op": "explode"},
		"bad cell":         {"op": "set_cell", "cell": "ZZZZZ99999999", "value": 1},
		"missing cell":     {"op": "set_cell", "value": 1},
		"empty format":     {"op": "format", "range": "A1:A2", "format": map[string]any{}},
		"bad colour":       {"op": "format", "range": "A1:A2", "format": map[string]any{"fill": "red"}},
		"bad chart range":  {"op": "add_chart", "cell": "E2", "chart": map[string]any{"type": "line", "data_range": "A1:A5"}},
		"bad chart type":   {"op": "add_chart", "cell": "E2", "chart": map[string]any{"type": "radar", "data_range": "A1:B5"}},
		"duplicate sheet":  {"op": "add_sheet", "name": "备注"},
		"illegal sheet":    {"op": "add_sheet", "name": "a/b"},
		"delete last":      {"op": "delete_sheet", "sheet": "预算"},
		"huge range":       {"op": "format", "range": "A1:ZZ100000", "format": map[string]any{"bold": true}},
		"bad width":        {"op": "col_width", "columns": "A", "width": 0},
		"insert before 1 ": {"op": "insert_rows", "row": 0},
	} {
		ops := []map[string]any{op}
		if name == "delete last" {
			ops = []map[string]any{{"op": "delete_sheet", "sheet": "备注"}, op}
		}
		if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "b.xlsx", "operations": ops}); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	if after := mustRead(t, filepath.Join(root, "b.xlsx")); !bytes.Equal(after, before) {
		t.Error("rejected edits changed the file")
	}
}

func TestEditXlsxSaveAsAndNoBackup(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "src.xlsx")
	orig := mustRead(t, filepath.Join(root, "src.xlsx"))

	if _, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "src.xlsx", "save_as": "out/copy",
		"operations": []map[string]any{{"op": "set_cell", "cell": "A1", "value": "新标题"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(root, "src.xlsx")), orig) {
		t.Error("save_as changed the original")
	}
	if v, _ := readBook(t, root, "out/copy.xlsx").GetCellValue("预算", "A1"); v != "新标题" {
		t.Errorf("copy A1 = %q", v)
	}
	// save_as never overwrites.
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "src.xlsx", "save_as": "out/copy.xlsx",
		"operations": []map[string]any{{"op": "set_cell", "cell": "A1", "value": "x"}},
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("save_as over an existing file: %v", err)
	}

	if _, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "src.xlsx", "no_backup": true,
		"operations": []map[string]any{{"op": "set_cell", "cell": "A1", "value": "改"}},
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("no_backup still wrote %s", e.Name())
		}
	}
}

func TestEditedFormulasGetStoredResults(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "r.xlsx")
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "r.xlsx", "no_backup": true,
		"operations": []map[string]any{{"op": "set_cell", "sheet": "预算", "cell": "B2", "value": 900}},
	}); err != nil {
		t.Fatal(err)
	}
	// B4 was written by create_xlsx with a stored result of 1000; the edit changed B2, so
	// the stored result must be recalculated to 1300, not kept stale.
	f := readBook(t, root, "r.xlsx")
	if v, _ := f.GetCellValue("预算", "B4"); v != "1300" {
		t.Errorf("stored result of B4 = %q, want 1300", v)
	}
}
