package officedoc

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"regexp"
	"strings"
)

// Some producers (openpyxl, Apache POI and the exporters built on it) write
// relationship targets as absolute part names: Target="/xl/drawings/drawing1.xml".
// Excel accepts both forms, but excelize's drawing code only follows relative ones
// ("../drawings/drawing1.xml"), so deleting a chart of such a file silently did
// nothing. The workbook is therefore opened with its targets made relative.

var absTargetRe = regexp.MustCompile(`(<Relationship\b[^>]*?\bTarget=")(/[^"]*)(")`)

// relativeRelTargets returns data with every absolute relationship target turned
// into the equivalent relative one. changed is false when there was none.
func relativeRelTargets(data []byte) (out []byte, changed bool, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, false, err
	}
	need := false
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, ".rels") {
			need = true
			break
		}
	}
	if !need {
		return data, false, nil
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, zf := range zr.File {
		if !strings.HasSuffix(zf.Name, ".rels") || zf.UncompressedSize64 > maxPartBytes {
			if err := zw.Copy(zf); err != nil {
				return data, false, err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return data, false, err
		}
		src, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return data, false, err
		}
		dir := relSourceDir(zf.Name)
		patched := absTargetRe.ReplaceAllFunc(src, func(m []byte) []byte {
			sub := absTargetRe.FindSubmatch(m)
			return []byte(string(sub[1]) + relativeTarget(dir, string(sub[2])) + string(sub[3]))
		})
		if !bytes.Equal(patched, src) {
			changed = true
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: zf.Name, Method: zip.Deflate, Modified: zf.Modified})
		if err != nil {
			return data, false, err
		}
		if _, err := w.Write(patched); err != nil {
			return data, false, err
		}
	}
	if err := zw.Close(); err != nil {
		return data, false, err
	}
	if !changed {
		return data, false, nil
	}
	return buf.Bytes(), true, nil
}

// relSourceDir is the directory of the part a .rels file describes:
// xl/worksheets/_rels/sheet1.xml.rels -> xl/worksheets; _rels/.rels -> "".
func relSourceDir(rels string) string {
	dir := path.Dir(rels) // .../_rels
	if path.Base(dir) == "_rels" {
		dir = path.Dir(dir)
	}
	if dir == "." {
		return ""
	}
	return dir
}

// relativeTarget expresses the part name target ("/xl/drawings/d.xml") relative to dir.
func relativeTarget(dir, target string) string {
	to := strings.Split(strings.TrimPrefix(path.Clean(target), "/"), "/")
	var from []string
	if dir != "" {
		from = strings.Split(dir, "/")
	}
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(to)-i)
	for range from[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, to[i:]...)
	return strings.Join(parts, "/")
}
