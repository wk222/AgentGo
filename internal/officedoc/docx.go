// Package officedoc writes Office documents (Word .docx, Excel .xlsx) natively in
// Go: no Office install, no Python, no sidecar process. It is the document
// generation half of the local office agent; reading documents stays with the
// parse_file tool.
//
// docx.go is a small WordprocessingML writer. It emits only the parts a report
// needs (headings, paragraphs with inline styling, nested bullet / numbered
// lists, tables, code blocks, quotes, page breaks) and relies on a fixed style
// sheet, so the output is predictable and opens in Word, WPS and LibreOffice.
package officedoc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Run is a stretch of text with one set of inline styling.
type Run struct {
	Text   string
	Bold   bool
	Italic bool
	Strike bool
	Code   bool // monospace with a light background
	Break  bool // a line break; Text is ignored
}

// Doc builds one .docx. The zero value is ready to use.
type Doc struct {
	Title  string // document property (shown in file info), not a visible heading
	Author string

	body    bytes.Buffer
	listSeq int // numbered lists started so far (each restarts at 1)
	blocks  int
	lastTbl bool
}

// Blocks reports how many top-level blocks were added.
func (d *Doc) Blocks() int { return d.blocks }

// Heading adds a heading of the given level (1-6).
func (d *Doc) Heading(level int, runs []Run) {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	d.para(fmt.Sprintf("Heading%d", level), "", runs)
}

// Paragraph adds a normal paragraph.
func (d *Doc) Paragraph(runs []Run) { d.para("", "", runs) }

// Quote adds an indented block quote paragraph.
func (d *Doc) Quote(runs []Run) { d.para("Quote", "", runs) }

// CodeBlock adds preformatted lines in a monospace, shaded style.
func (d *Doc) CodeBlock(lines []string) {
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, l := range lines {
		d.para("Code", "", []Run{{Text: l}})
	}
	d.blocks -= len(lines) - 1 // one block, however many lines
}

// Rule adds a horizontal rule.
func (d *Doc) Rule() {
	d.flushTable()
	d.body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="A6A6A6"/></w:pBdr></w:pPr></w:p>`)
	d.blocks++
}

// PageBreak starts a new page.
func (d *Doc) PageBreak() {
	d.flushTable()
	d.body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
	d.blocks++
}

// NewNumberedList starts a numbered list that counts from 1 and returns its id
// for ListItem. Bullet lists need no id (pass 0).
func (d *Doc) NewNumberedList() int {
	d.listSeq++
	return d.listSeq
}

// ListItem adds one list item at the given nesting level (0-8). numberedID is
// the id from NewNumberedList, or 0 for a bullet.
func (d *Doc) ListItem(numberedID, level int, runs []Run) {
	if level < 0 {
		level = 0
	}
	if level > 8 {
		level = 8
	}
	numID := 1 // bullets
	if numberedID > 0 {
		numID = 1 + numberedID
	}
	d.para("ListParagraph", fmt.Sprintf(`<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`, level, numID), runs)
}

// Table adds a table. The first row is the header when header is true. Cells
// hold runs; short rows are padded to the widest row.
func (d *Doc) Table(rows [][][]Run, header bool) {
	d.flushTable()
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return
	}
	const total = 9000 // dxa: roughly the text width of an A4 page
	w := total / cols
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="autofit"/></w:tblPr><w:tblGrid>`)
	for i := 0; i < cols; i++ {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, w)
	}
	b.WriteString(`</w:tblGrid>`)
	for ri, r := range rows {
		isHead := header && ri == 0
		b.WriteString(`<w:tr>`)
		if isHead {
			b.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for ci := 0; ci < cols; ci++ {
			var cell []Run
			if ci < len(r) {
				cell = r[ci]
			}
			b.WriteString(`<w:tc><w:tcPr>`)
			fmt.Fprintf(&b, `<w:tcW w:w="%d" w:type="dxa"/>`, w)
			if isHead {
				b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="D9E2F3"/>`)
			}
			b.WriteString(`</w:tcPr>`)
			if isHead {
				cell = bolded(cell)
			}
			b.WriteString(paraXML("", "", cell))
			b.WriteString(`</w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	d.body.WriteString(b.String())
	d.blocks++
	d.lastTbl = true
}

func bolded(runs []Run) []Run {
	out := make([]Run, len(runs))
	for i, r := range runs {
		r.Bold = true
		out[i] = r
	}
	return out
}

// flushTable puts the paragraph Word requires after a table.
func (d *Doc) flushTable() {
	if d.lastTbl {
		d.body.WriteString(`<w:p/>`)
		d.lastTbl = false
	}
}

func (d *Doc) para(style, extraPPr string, runs []Run) {
	d.flushTable()
	d.body.WriteString(paraXML(style, extraPPr, runs))
	d.blocks++
}

func paraXML(style, extraPPr string, runs []Run) string {
	var b strings.Builder
	b.WriteString(`<w:p>`)
	if style != "" || extraPPr != "" {
		b.WriteString(`<w:pPr>`)
		if style != "" {
			fmt.Fprintf(&b, `<w:pStyle w:val="%s"/>`, style)
		}
		b.WriteString(extraPPr)
		b.WriteString(`</w:pPr>`)
	}
	for _, r := range runs {
		if r.Break {
			b.WriteString(`<w:r><w:br/></w:r>`)
			continue
		}
		if r.Text == "" {
			continue
		}
		b.WriteString(`<w:r>`)
		var pr strings.Builder
		if r.Code {
			pr.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Microsoft YaHei"/>`)
		}
		if r.Bold {
			pr.WriteString(`<w:b/>`)
		}
		if r.Italic {
			pr.WriteString(`<w:i/>`)
		}
		if r.Strike {
			pr.WriteString(`<w:strike/>`)
		}
		if r.Code {
			pr.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`)
		}
		if pr.Len() > 0 {
			b.WriteString(`<w:rPr>` + pr.String() + `</w:rPr>`)
		}
		b.WriteString(`<w:t xml:space="preserve">` + escapeXML(r.Text) + `</w:t></w:r>`)
	}
	b.WriteString(`</w:p>`)
	return b.String()
}

// escapeXML escapes text for an XML 1.0 document and drops the control
// characters XML cannot carry at all (a stray \x00 or \x1b from model output
// would otherwise make Word report the file as corrupt).
func escapeXML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch {
		case r == utf8.RuneError && size == 1:
			continue
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFE || r == 0xFFFF:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// WriteTo writes the finished package. A Doc with no blocks still yields a
// valid (empty) document.
func (d *Doc) WriteTo(w io.Writer) (int64, error) {
	d.flushTable()
	cw := &countWriter{w: w}
	zw := zip.NewWriter(cw)
	now := time.Now().UTC()
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypesXML},
		{"_rels/.rels", rootRelsXML},
		{"docProps/core.xml", d.coreXML(now)},
		{"word/_rels/document.xml.rels", docRelsXML},
		{"word/styles.xml", stylesXML},
		{"word/numbering.xml", numberingXML(d.listSeq)},
		{"word/document.xml", d.documentXML()},
	}
	for _, p := range parts {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return cw.n, err
		}
		if _, err := io.WriteString(f, p.body); err != nil {
			return cw.n, err
		}
	}
	if err := zw.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (d *Doc) documentXML() string {
	return xmlHeader + `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` +
		d.body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/>` +
		`</w:sectPr></w:body></w:document>`
}

func (d *Doc) coreXML(now time.Time) string {
	author := d.Author
	if author == "" {
		author = "AgentGo"
	}
	ts := now.Format("2006-01-02T15:04:05Z")
	return xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + escapeXML(d.Title) + `</dc:title><dc:creator>` + escapeXML(author) + `</dc:creator>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:modified></cp:coreProperties>`
}
