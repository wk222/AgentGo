package officedoc

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestToNumberConvertsOnlyRealNumbersWrittenAsText(t *testing.T) {
	root, ts := newXlsxTools(t)
	f := excelize.NewFile()
	for cell, v := range map[string]any{
		"A1": "93.8%", "A2": "1,234", "A3": "￥12", "A4": "007", "A5": "n/a", "A6": 5.5, "A7": " 91.0% ", "A8": "12.345%",
		"A9": "123456789012345",
	} {
		_ = f.SetCellValue("Sheet1", cell, v)
	}
	if err := f.SaveAs(filepath.Join(root, "t.xlsx")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "t.xlsx", "save_as": "out.xlsx", "operations": []map[string]any{
		{"op": "to_number", "range": "A1:A9"},
	}}); err != nil {
		t.Fatal(err)
	}
	g, err := excelize.OpenFile(filepath.Join(root, "out.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	typ := func(cell string) excelize.CellType { c, _ := g.GetCellType("Sheet1", cell); return c }
	raw := func(cell string) string {
		v, _ := g.GetCellValue("Sheet1", cell, excelize.Options{RawCellValue: true})
		return v
	}
	shown := func(cell string) string { v, _ := g.GetCellValue("Sheet1", cell); return v }

	for cell, want := range map[string]string{"A1": "0.938", "A2": "1234", "A3": "12", "A7": "0.91", "A8": "0.12345", "A6": "5.5"} {
		if typ(cell) == excelize.CellTypeSharedString || typ(cell) == excelize.CellTypeInlineString {
			t.Errorf("%s is still text", cell)
		}
		if raw(cell) != want {
			t.Errorf("%s = %q, want %q", cell, raw(cell), want)
		}
	}
	// The text percentage shows the same digits it had.
	for cell, want := range map[string]string{"A1": "93.8%", "A7": "91.0%", "A8": "12.345%"} {
		if shown(cell) != want {
			t.Errorf("%s shows %q, want %q", cell, shown(cell), want)
		}
	}
	for _, cell := range []string{"A4", "A5", "A9"} { // identifiers and non-numbers stay as they were
		if raw(cell) == "" || (typ(cell) != excelize.CellTypeSharedString && typ(cell) != excelize.CellTypeInlineString) {
			t.Errorf("%s should have stayed text, type %v value %q", cell, typ(cell), raw(cell))
		}
	}
}

func TestAnalyzeReportsHowPercentsAndDatesAreStored(t *testing.T) {
	root, ts := newXlsxTools(t)
	f := excelize.NewFile()
	pct, _ := f.NewStyle(&excelize.Style{CustomNumFmt: ptr("0.0%")})
	mon, _ := f.NewStyle(&excelize.Style{CustomNumFmt: ptr("yyyy-mm")})
	for i, h := range []string{"地市", "月份", "覆盖率"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("Sheet1", cell, h)
	}
	for r := 2; r <= 7; r++ {
		a, b, c := cellName(1, r), cellName(2, r), cellName(3, r)
		_ = f.SetCellValue("Sheet1", a, []string{"甲", "乙"}[r%2])
		_ = f.SetCellValue("Sheet1", b, time.Date(2026, time.Month(r), 1, 0, 0, 0, 0, time.UTC))
		_ = f.SetCellStyle("Sheet1", b, b, mon)
		_ = f.SetCellValue("Sheet1", c, 0.9+float64(r)/100)
		_ = f.SetCellStyle("Sheet1", c, c, pct)
	}
	if err := f.SaveAs(filepath.Join(root, "p.xlsx")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := analyze(t, ts, map[string]any{"relative_path": "p.xlsx", "queries": []map[string]any{{"op": "profile"}}})
	byName := map[string]aColInfo{}
	for _, c := range out.Results[0].Profile {
		byName[c.Name] = c
	}
	if got := byName["月份"].Stored; !strings.Contains(got, "real dates") || !strings.Contains(got, "TEXT(") {
		t.Errorf("month stored = %q", got)
	}
	if got := byName["覆盖率"].Stored; !strings.Contains(got, "fractions") || !strings.Contains(got, "0.96") {
		t.Errorf("percent stored = %q", got)
	}
	if got := byName["地市"].Stored; got != "" {
		t.Errorf("a text column needs no stored note, got %q", got)
	}
}

func TestEditXlsxSaveAsOverwritesOnlyWhenAsked(t *testing.T) {
	root, _ := seedSeries(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	edit := func(extra map[string]any) error {
		args := map[string]any{"relative_path": "s.xlsx", "save_as": "o.xlsx", "operations": []map[string]any{
			{"op": "set_cell", "sheet": "S", "cell": "E1", "value": "x"},
		}}
		for k, v := range extra {
			args[k] = v
		}
		_, err := run(t, ToolEditXlsx, ts, args)
		return err
	}
	if err := edit(nil); err != nil {
		t.Fatal(err)
	}
	if err := edit(nil); err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("a second save_as to the same file must fail and name the option: %v", err)
	}
	if err := edit(map[string]any{"overwrite": true}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestCopyFormatGivesANewColumnTheNeighboursLook(t *testing.T) {
	root, _ := seedSeries(t)
	ts, _ := Tools(Env{Resolve: jailTo(root)})
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "o.xlsx", "operations": []map[string]any{
		{"op": "set_cell", "sheet": "S", "cell": "D1", "value": "较上月"},
		{"op": "copy_format", "sheet": "S", "cell": "C1", "range": "D1"},
		{"op": "copy_format", "sheet": "S", "cell": "C2", "range": "D2:D7"},
	}}); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(filepath.Join(root, "o.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for src, dst := range map[string]string{"C1": "D1", "C2": "D2", "C2 ": "D7"} {
		a, _ := f.GetCellStyle("S", strings.TrimSpace(src))
		b, _ := f.GetCellStyle("S", dst)
		if a != b {
			t.Errorf("%s style %d, want the style of %s (%d)", dst, b, src, a)
		}
	}
	if h, _ := f.GetCellStyle("S", "D1"); h == 0 {
		t.Error("the header style was not copied: create_xlsx styles its header row")
	}
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{"relative_path": "s.xlsx", "save_as": "bad.xlsx", "operations": []map[string]any{
		{"op": "copy_format", "sheet": "S", "range": "D1"},
	}}); err == nil {
		t.Error("copy_format without a source cell must fail")
	}
}

func ptr[T any](v T) *T { return &v }

func cellName(c, r int) string { s, _ := excelize.CoordinatesToCellName(c, r); return s }
