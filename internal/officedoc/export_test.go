package officedoc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExportSamples writes one sample of each generated format so they can be
// opened by hand in PowerPoint, Excel or WPS (or read with python-pptx and
// openpyxl) whenever the writers change:
//
//	OFFICEDOC_EXPORT_DIR=<dir> go test -run TestExportSamples ./internal/officedoc
func TestExportSamples(t *testing.T) {
	dir := os.Getenv("OFFICEDOC_EXPORT_DIR")
	if dir == "" {
		t.Skip("set OFFICEDOC_EXPORT_DIR to export sample files")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, deckData := buildSampleDeck(t)
	if err := os.WriteFile(filepath.Join(dir, "sample-deck.pptx"), deckData, 0o644); err != nil {
		t.Fatal(err)
	}

	root, ts := newXlsxTools(t)
	seedBook(t, ts, "budget.xlsx")
	if _, err := run(t, ToolEditXlsx, ts, map[string]any{
		"relative_path": "budget.xlsx", "no_backup": true,
		"operations": []map[string]any{
			{"op": "format", "sheet": "预算", "range": "B2:B4", "format": map[string]any{"num_fmt": "#,##0.00"}},
			{"op": "format", "sheet": "预算", "range": "C2:C4", "format": map[string]any{"num_fmt": "0.0%"}},
			{"op": "add_chart", "sheet": "预算", "cell": "E2", "chart": map[string]any{"type": "column", "data_range": "A1:B3", "title": "预算构成"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample-budget.xlsx"), mustRead(t, filepath.Join(root, "budget.xlsx")), 0o644); err != nil {
		t.Fatal(err)
	}
}
