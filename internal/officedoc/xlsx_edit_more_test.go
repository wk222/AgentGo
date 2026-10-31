package officedoc

import (
	"archive/zip"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestShiftFormulaMovesRelativeReferencesOnly(t *testing.T) {
	for _, c := range []struct {
		in     string
		dr, dc int
		want   string
	}{
		{`IF(A5=A4,C5-C4,"")`, 1, 0, `IF(A6=A5,C6-C5,"")`},
		{`$C$4+C4`, 2, 1, `$C$4+D6`},
		{`C$4*$B4`, 3, 2, `E$4*$B7`},
		{`SUM(A1:A3)`, 1, 0, `SUM(A2:A4)`},
		{`'Q1 sheet'!A1+LOG10(B2)+"A1"`, 1, 0, `'Q1 sheet'!A2+LOG10(B3)+"A1"`},
		{`Q1!A1+Sheet2!B2`, 1, 0, `Q1!A2+Sheet2!B3`},
		{`TEXT(B4,"yyyy-mm")&"x"`, 1, 0, `TEXT(B5,"yyyy-mm")&"x"`},
		{`IF(A1="He said ""A1""",1,0)`, 1, 0, `IF(A2="He said ""A1""",1,0)`},
		{`SUM(C:C)+ROW()`, 5, 0, `SUM(C:C)+ROW()`},
	} {
		got, err := shiftFormula(c.in, c.dr, c.dc)
		if err != nil || got != c.want {
			t.Errorf("shiftFormula(%q, %d, %d) = %q, %v; want %q", c.in, c.dr, c.dc, got, err, c.want)
		}
	}
	if _, err := shiftFormula(`A1+B1`, -1, 0); err == nil || !strings.Contains(err.Error(), "outside the sheet") {
		t.Errorf("a reference pushed above row 1 must be an error: %v", err)
	}
}

func seedSeries(t *testing.T) (string, string) {
	t.Helper()
	root, ts := newXlsxTools(t)
	_, err := run(t, ToolCreateXlsx, ts, map[string]any{"relative_path": "s.xlsx", "sheets": []map[string]any{{"name": "S", "rows": [][]any{
		{"地市", "月", "值"},
		{"甲", 1, 10}, {"甲", 2, 12}, {"甲", 3, 11},
		{"乙", 1, 5}, {"乙", 2, nil}, {"乙", 3, 9},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "s.xlsx")
}

func TestFillFormulaFillsDownLikeExcel(t *testing.T) {
	root, _ := seedSeries(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	_, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "out.xlsx", "operations": []map[string]any{
		{"op": "set_cell", "sheet": "S", "cell": "D1", "value": "较上月"},
		{"op": "fill_formula", "sheet": "S", "range": "D2:D7", "formula": `=IF(AND(A2=A1,ISNUMBER(C2),ISNUMBER(C1)),C2-C1,"")`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(filepath.Join(root, "out.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if form, _ := f.GetCellFormula("S", "D5"); form != `IF(AND(A5=A4,ISNUMBER(C5),ISNUMBER(C4)),C5-C4,"")` {
		t.Errorf("D5 formula = %q", form)
	}
	// 甲/1 starts a city, 甲/2 and 甲/3 compare with the month before, 乙 is blank in month 2.
	for cell, want := range map[string]string{"D2": "", "D3": "2", "D4": "-1", "D5": "", "D6": "", "D7": ""} {
		if got, _ := f.CalcCellValue("S", cell); got != want {
			t.Errorf("%s = %q, want %q", cell, got, want)
		}
	}
	// A formula that cannot be filled (a reference above row 1) fails the whole edit.
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "bad.xlsx", "operations": []map[string]any{
		{"op": "fill_formula", "sheet": "S", "range": "D1:D3", "formula": "=A1+C1-C0"},
	}}); err == nil {
		t.Error("an unfillable formula must be an error")
	}
}

func TestConditionalFormatMarksCellsByRule(t *testing.T) {
	root, _ := seedSeries(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	_, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "cf.xlsx", "operations": []map[string]any{
		{"op": "conditional_format", "sheet": "S", "range": "C2:C7", "format": map[string]any{"fill": "#FFC7CE", "font_color": "#9C0006"},
			"rule": map[string]any{"type": "cell", "criteria": "<", "value": 10}},
		{"op": "conditional_format", "sheet": "S", "range": "A2:C7", "format": map[string]any{"fill": "#FFEB9C"},
			"rule": map[string]any{"type": "formula", "formula": `=AND($A2="乙",$B2=3)`}},
		{"op": "conditional_format", "sheet": "S", "range": "B2:B7", "format": map[string]any{"bold": true},
			"rule": map[string]any{"type": "cell", "criteria": "between", "value": 2, "value2": 3}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(filepath.Join(root, "cf.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := f.GetConditionalFormats("S")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, want := range []string{`"C2:C7"`, `"A2:C7"`, `"B2:B7"`, `"Type":"cell"`, `"Type":"formula"`, `AND($A2=\"乙\",$B2=3)`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("conditional formats %s lack %s", raw, want)
		}
	}
	// The file stores a rule's formula without the "=": WPS ignores a rule that has it.
	if xml := sheetXML(t, filepath.Join(root, "cf.xlsx")); !strings.Contains(xml, `<formula>AND($A2=`) || strings.Contains(xml, `<formula>=`) {
		t.Errorf("a formula rule must be stored without a leading =: %s", xml)
	}
	if opts := got["C2:C7"]; len(opts) != 1 || opts[0].Criteria != "less than" || opts[0].Value != "10" || opts[0].Format == nil {
		t.Errorf("C2:C7 rule = %+v", opts)
	}
	if opts := got["B2:B7"]; len(opts) != 1 || opts[0].Criteria != "between" || opts[0].MinValue != "2" || opts[0].MaxValue != "3" {
		t.Errorf("B2:B7 rule = %+v", opts)
	}
	for name, op := range map[string]map[string]any{
		"no colour":    {"op": "conditional_format", "sheet": "S", "range": "C2:C7", "format": map[string]any{}, "rule": map[string]any{"type": "cell", "criteria": ">", "value": 1}},
		"no rule":      {"op": "conditional_format", "sheet": "S", "range": "C2:C7", "format": map[string]any{"fill": "#FF0000"}},
		"bad criteria": {"op": "conditional_format", "sheet": "S", "range": "C2:C7", "format": map[string]any{"fill": "#FF0000"}, "rule": map[string]any{"type": "cell", "criteria": "~", "value": 1}},
		"bad type":     {"op": "conditional_format", "sheet": "S", "range": "C2:C7", "format": map[string]any{"fill": "#FF0000"}, "rule": map[string]any{"type": "color_scale"}},
	} {
		if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "no.xlsx", "operations": []map[string]any{op}}); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

// sheetXML returns the XML of the first worksheet of an .xlsx file.
func sheetXML(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet1.xml" {
			rc, err := zf.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatal("no sheet1.xml")
	return ""
}
