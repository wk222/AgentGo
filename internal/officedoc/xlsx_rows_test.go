package officedoc

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func styleAt(t *testing.T, path, sheet, cell string) int {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A row inserted among data rows looks like them (what Excel does), so a new line
// does not come out without the number format its neighbours have. Directly under
// the header it follows the first data row, not the header.
func TestInsertRowsInheritsTheNeighbouringRowFormat(t *testing.T) {
	root, ts := newXlsxTools(t)
	seedBook(t, ts, "b.xlsx")
	path := filepath.Join(root, "b.xlsx")
	if err := editOps(ts, t, "b.xlsx",
		map[string]any{"op": "format", "sheet": "预算", "range": "B2:B3", "format": map[string]any{"num_fmt": "#,##0.00"}},
		map[string]any{"op": "format", "sheet": "预算", "range": "A4:C4", "format": map[string]any{"bold": true}},
	); err != nil {
		t.Fatal(err)
	}
	data := styleAt(t, path, "预算", "B3")
	total := styleAt(t, path, "预算", "B4")
	header := styleAt(t, path, "预算", "B1")
	if data == 0 || data == total || data == header {
		t.Fatalf("test setup: styles data=%d total=%d header=%d", data, total, header)
	}

	// Before the total row: the model is the data row above it, not the total.
	if err := editOps(ts, t, "b.xlsx", map[string]any{"op": "insert_rows", "sheet": "预算", "row": 4, "count": 2}); err != nil {
		t.Fatal(err)
	}
	for _, cell := range []string{"B4", "B5"} {
		if got := styleAt(t, path, "预算", cell); got != data {
			t.Errorf("%s style = %d, want the data row's %d", cell, got, data)
		}
	}
	if got := styleAt(t, path, "预算", "B6"); got != total {
		t.Errorf("the total row, now row 6, has style %d, want its own %d", got, total)
	}

	// Under the header: the first data row, not the header.
	if err := editOps(ts, t, "b.xlsx", map[string]any{"op": "insert_rows", "sheet": "预算", "row": 2}); err != nil {
		t.Fatal(err)
	}
	if got := styleAt(t, path, "预算", "B2"); got != data {
		t.Errorf("B2 style = %d, want the first data row's %d (the header is %d)", got, data, header)
	}
	if got := styleAt(t, path, "预算", "B1"); got != header {
		t.Errorf("the header changed: %d, want %d", got, header)
	}

	// Above the first row there is nothing to copy.
	if err := editOps(ts, t, "b.xlsx", map[string]any{"op": "insert_rows", "sheet": "预算", "row": 1}); err != nil {
		t.Fatal(err)
	}
	if got := styleAt(t, path, "预算", "B1"); got != 0 {
		t.Errorf("a row inserted above the header has style %d, want none", got)
	}
}
