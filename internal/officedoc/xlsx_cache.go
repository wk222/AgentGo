package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// excelize writes a formula cell without any stored result. Excel and WPS
// calculate on open, but file previewers (chat apps, Explorer, phones) do not
// and show blanks. finishWorkbook therefore stores the computed value next to
// every formula and also marks the workbook for a full recalculation on open,
// so a stored value can never be shown stale by a real spreadsheet program.
const (
	maxCachedFormulas = 20000
	cacheBudget       = 4 * time.Second
)

// formulaCellRe matches a plain formula cell as excelize writes it, with or
// without a stored value: a workbook that was opened and edited still carries
// the old results, which an edit may have made stale. Shared and array
// formulas (<f> with attributes) are not matched and stay as they are.
var formulaCellRe = regexp.MustCompile(`<c r="([A-Z]{1,3}[0-9]{1,7})"((?: s="[0-9]+")?)(?: t="(?:str|n|b|e)")?><f>([^<]*)</f>(?:<v>[^<]*</v>|<v/>)?</c>`)

// finishWorkbook serializes f with recalculation-on-open and stored results.
// Storing results is best effort: any failure falls back to the plain file.
func finishWorkbook(f *excelize.File) ([]byte, error) {
	full := true
	_ = f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &full})
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	if out, err := cacheFormulaValues(f, buf.Bytes()); err == nil {
		return out, nil
	}
	return buf.Bytes(), nil
}

func cacheFormulaValues(f *excelize.File, data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sheets, err := sheetFiles(zr)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	deadline := time.Now().Add(cacheBudget)
	left := maxCachedFormulas
	for _, zf := range zr.File {
		name, isSheet := sheets[zf.Name]
		if !isSheet {
			if err := zw.Copy(zf); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		src, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		patched := formulaCellRe.ReplaceAllFunc(src, func(m []byte) []byte {
			sub := formulaCellRe.FindSubmatch(m)
			cell, style, formula := string(sub[1]), string(sub[2]), string(sub[3])
			if left <= 0 || time.Now().After(deadline) {
				return []byte(uncachedCellXML(cell, style, formula))
			}
			left--
			res, err := f.CalcCellValue(name, cell, excelize.Options{RawCellValue: true})
			return []byte(cachedCellXML(cell, style, formula, res, err))
		})
		w, err := zw.CreateHeader(&zip.FileHeader{Name: zf.Name, Method: zip.Deflate, Modified: zf.Modified})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(patched); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// uncachedCellXML renders a formula cell with no stored result, the form
// excelize itself writes. A spreadsheet program calculates it on open.
func uncachedCellXML(cell, style, formula string) string {
	return `<c r="` + cell + `"` + style + ` t="str"><f>` + formula + `</f></c>`
}

// cachedCellXML renders a formula cell with its result. formula is already
// XML-escaped (it was cut from the sheet XML). When the result is unknown (a
// function excelize does not implement) the cell is stored without one rather
// than with a stale one.
func cachedCellXML(cell, style, formula, res string, calcErr error) string {
	head := `<c r="` + cell + `"` + style
	if calcErr != nil {
		if msg := calcErr.Error(); isExcelError(msg) {
			return head + ` t="e"><f>` + formula + `</f><v>` + escapeXML(msg) + `</v></c>`
		}
		return uncachedCellXML(cell, style, formula)
	}
	switch {
	case res == "TRUE" || res == "FALSE":
		b := "0"
		if res == "TRUE" {
			b = "1"
		}
		return head + ` t="b"><f>` + formula + `</f><v>` + b + `</v></c>`
	case isPlainNumber(res):
		return head + `><f>` + formula + `</f><v>` + res + `</v></c>`
	default:
		return head + ` t="str"><f>` + formula + `</f><v>` + escapeXML(res) + `</v></c>`
	}
}

func isExcelError(s string) bool {
	switch s {
	case "#DIV/0!", "#N/A", "#NAME?", "#NULL!", "#NUM!", "#REF!", "#VALUE!":
		return true
	}
	return false
}

func isPlainNumber(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	v, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && !strings.ContainsAny(s, "xXpP_")
}

// sheetFiles maps the zip path of each worksheet part to its sheet name.
func sheetFiles(zr *zip.Reader) (map[string]string, error) {
	read := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s missing", name)
	}
	wbData, err := read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	relData, err := read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wbData, &wb); err != nil {
		return nil, err
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(relData, &rels); err != nil {
		return nil, err
	}
	target := map[string]string{}
	for _, r := range rels.Rel {
		target[r.ID] = r.Target
	}
	out := map[string]string{}
	for _, s := range wb.Sheets {
		t := target[s.RID]
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "/") {
			t = strings.TrimPrefix(t, "/")
		} else {
			t = path.Join("xl", t)
		}
		out[t] = s.Name
	}
	return out, nil
}
