package officedoc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	maxTableCols = 8
	maxTableRows = 80
	cellPadX     = 0.12 // inches, each side
	cellPadY     = 0.07
)

var numberStripper = strings.NewReplacer(",", "", "，", "", "%", "", "¥", "", "￥", "", "$", "", "+", "", "万", "", "亿", "", "元", "", " ", "")

// isNumericCell reports whether s reads as a number (so its column is right-aligned).
func isNumericCell(s string) bool {
	t := numberStripper.Replace(strings.TrimSpace(s))
	if t == "" || t == "-" {
		return false
	}
	_, err := strconv.ParseFloat(strings.TrimPrefix(t, "-"), 64)
	return err == nil
}

func stringRows(in pptxSlideIn) ([][]string, error) {
	cols := len(in.Columns)
	if cols < 1 || cols > maxTableCols {
		return nil, fmt.Errorf("columns needs 1 to %d header cells, got %d", maxTableCols, cols)
	}
	if len(in.Rows) == 0 {
		return nil, errors.New("rows is required")
	}
	if len(in.Rows) > maxTableRows {
		return nil, fmt.Errorf("too many rows: %d (max %d per table; summarise or use two tables)", len(in.Rows), maxTableRows)
	}
	rows := make([][]string, len(in.Rows))
	for i, r := range in.Rows {
		if len(r) != cols {
			return nil, fmt.Errorf("rows[%d] has %d cells but columns has %d", i, len(r), cols)
		}
		rows[i] = make([]string, cols)
		for j, v := range r {
			rows[i][j] = strings.TrimSpace(cellText(v))
		}
	}
	return rows, nil
}

// columnWidths shares width (the width of the content area) between the columns by how much text each holds.
func columnWidths(header []string, rows [][]string, width float64) []float64 {
	weights := make([]float64, len(header))
	for c := range header {
		w := textEm(header[c])
		for _, r := range rows {
			w = max(w, textEm(r[c]))
		}
		weights[c] = min(max(w, 3), 26) + 2
	}
	var sum float64
	for _, w := range weights {
		sum += w
	}
	out := make([]float64, len(weights))
	for i, w := range weights {
		out[i] = max(width*w/sum, 0.8)
	}
	// Renormalise after applying the minimum.
	var total float64
	for _, w := range out {
		total += w
	}
	for i := range out {
		out[i] *= width / total
	}
	return out
}

// textEm is the width of the longest line of s in em.
func textEm(s string) float64 {
	var longest float64
	for _, line := range strings.Split(s, "\n") {
		var w float64
		for _, r := range line {
			w += charEm(r)
		}
		longest = max(longest, w)
	}
	return longest
}

func rowHeight(cells []string, widths []float64, size float64) float64 {
	lines := 1
	for i, c := range cells {
		avail := (widths[i] - 2*cellPadX) * ptPerInch / size
		lines = max(lines, countLines(c, avail))
	}
	return float64(lines)*size*lineFactor/ptPerInch + 2*cellPadY
}

func tableHeight(header []string, rows [][]string, widths []float64, size float64) float64 {
	h := rowHeight(header, widths, size)
	for _, r := range rows {
		h += rowHeight(r, widths, size)
	}
	return h
}

// paginate splits rows into runs that fit availH at the smallest table font.
func paginate(header []string, rows [][]string, widths []float64, availH, minSize float64) [][][]string {
	var chunks [][][]string
	var cur [][]string
	used := rowHeight(header, widths, minSize)
	for _, r := range rows {
		h := rowHeight(r, widths, minSize)
		if len(cur) > 0 && used+h > availH {
			chunks = append(chunks, cur)
			cur, used = nil, rowHeight(header, widths, minSize)
		}
		cur = append(cur, r)
		used += h
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func (d *deck) tableSlides(in pptxSlideIn) ([]*slide, error) {
	rows, err := stringRows(in)
	if err != nil {
		return nil, err
	}
	header := make([]string, len(in.Columns))
	for i, c := range in.Columns {
		header[i] = strings.TrimSpace(c)
	}
	numeric := make([]bool, len(header))
	for c := range header {
		numeric[c] = true
		seen := false
		for _, r := range rows {
			if r[c] == "" {
				continue
			}
			seen = true
			if !isNumericCell(r[c]) {
				numeric[c] = false
				break
			}
		}
		numeric[c] = numeric[c] && seen
	}
	widths := columnWidths(header, rows, d.fr.w)
	caption := strings.TrimSpace(in.Caption)
	availH := d.fr.h - 0.1
	if caption != "" {
		availH -= 0.45
	}
	chunks := paginate(header, rows, widths, availH, 12)
	if len(chunks) > 1 {
		d.warn("the table has %d rows and was split over %d slides", len(rows), len(chunks))
	}
	var out []*slide
	for k, chunk := range chunks {
		title := in.Title
		if len(chunks) > 1 {
			title = fmt.Sprintf("%s（%d/%d）", in.Title, k+1, len(chunks))
		}
		size := 12.0
		for _, sz := range []float64{16, 15, 14, 13, 12} {
			if tableHeight(header, chunk, widths, sz) <= availH {
				size = sz
				break
			}
		}
		s := d.contentSlide(layoutTitleOnly, title, in.Notes)
		h := tableHeight(header, chunk, widths, size)
		id := s.id()
		fmt.Fprintf(&s.shapes, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d" descr="%s"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`+
			`<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1"/><a:tblGrid>`,
			id, id, escapeXML(in.Title), emu(d.fr.x), emu(d.fr.y), emu(d.fr.w), emu(h))
		for _, w := range widths {
			fmt.Fprintf(&s.shapes, `<a:gridCol w="%d"/>`, emu(w))
		}
		s.shapes.WriteString(`</a:tblGrid>`)
		d.tableRow(s, header, widths, rowHeight(header, widths, size), size, numeric, -1)
		for i, r := range chunk {
			d.tableRow(s, r, widths, rowHeight(r, widths, size), size, numeric, i)
		}
		s.shapes.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
		if caption != "" {
			s.box(d.fr.x, d.fr.y+h+0.1, d.fr.w, 0.4, []para{{text: caption, size: 12, color: d.pal.Muted}},
				boxOpts{name: "Caption", anchor: "ctr", insetX: 0.05, insetY: 0.02})
		}
		out = append(out, s.finish())
	}
	return out, nil
}

// tableRow writes one row; index -1 is the header.
func (d *deck) tableRow(s *sb, cells []string, widths []float64, h, size float64, numeric []bool, index int) {
	pal := d.pal
	fmt.Fprintf(&s.shapes, `<a:tr h="%d">`, emu(h))
	for i, text := range cells {
		fill, color, bold := "FFFFFF", pal.Text, false
		switch {
		case index < 0:
			fill, color, bold = pal.Primary, "FFFFFF", true
		case index%2 == 1:
			fill = pal.Surface
		}
		algn := "l"
		if numeric[i] {
			algn = "r"
		}
		p := para{text: text, size: size, color: color, bold: bold, align: algn}
		line := func(tag string, color string) string {
			if color == "" {
				return `<a:` + tag + ` w="0"><a:noFill/></a:` + tag + `>`
			}
			return `<a:` + tag + ` w="9525"><a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill></a:` + tag + `>`
		}
		s.shapes.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/>` + d.paraXML(p, pal.Accent) + `</a:txBody>` +
			fmt.Sprintf(`<a:tcPr marL="%d" marR="%d" marT="%d" marB="%d" anchor="ctr">`, emu(cellPadX), emu(cellPadX), emu(cellPadY), emu(cellPadY)) +
			line("lnL", "") + line("lnR", "") + line("lnT", "") + line("lnB", pal.Line) +
			`<a:solidFill><a:srgbClr val="` + fill + `"/></a:solidFill></a:tcPr></a:tc>`)
	}
	s.shapes.WriteString(`</a:tr>`)
}
