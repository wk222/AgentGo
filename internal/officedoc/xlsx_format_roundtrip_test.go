package officedoc

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Workbooks written by openpyxl or Excel carry an empty <patternFill/> for "no
// fill"; formatting a cell that already has such a style used to fail with
// "fill pattern value must be between 0 and 18".
func TestFormatKeepsWorkingOnCellsStyledByOtherWriters(t *testing.T) {
	src := excelize.NewFile()
	_ = src.SetCellValue("Sheet1", "A1", 0.5)
	if err := applyFormat(src, "Sheet1", "A1:B2", &xlsxFormat{NumFmt: "0.00", Align: "center"}); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := src.Write(&raw); err != nil {
		t.Fatal(err)
	}

	// Rewrite styles.xml the way openpyxl does.
	zr, err := zip.NewReader(bytes.NewReader(raw.Bytes()), int64(raw.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var patched bytes.Buffer
	zw := zip.NewWriter(&patched)
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if zf.Name == "xl/styles.xml" {
			data = []byte(strings.ReplaceAll(string(data), ` patternType="none"`, ""))
		}
		w, _ := zw.Create(zf.Name)
		_, _ = w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := excelize.OpenReader(bytes.NewReader(patched.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, _ := f.GetCellStyle("Sheet1", "A1")
	if st, _ := f.GetStyle(id); st == nil || st.Fill.Pattern != -1 {
		t.Fatalf("fixture does not reproduce the openpyxl-style fill: %+v", st)
	}

	yes := true
	for _, fm := range []*xlsxFormat{{Border: &yes}, {Fill: "#FF0000"}, {Bold: &yes}} {
		if err := applyFormat(f, "Sheet1", "A1:B2", fm); err != nil {
			t.Fatalf("format %+v: %v", fm, err)
		}
	}
	id, _ = f.GetCellStyle("Sheet1", "A1")
	st, _ := f.GetStyle(id)
	if st.CustomNumFmt == nil || *st.CustomNumFmt != "0.00" || st.Alignment == nil || st.Alignment.Horizontal != "center" {
		t.Fatalf("earlier formatting was lost: %+v", st)
	}
	if len(st.Fill.Color) == 0 || !strings.EqualFold(strings.TrimPrefix(st.Fill.Color[0], "#"), "FF0000") {
		t.Fatalf("fill not applied: %+v", st.Fill)
	}
}
