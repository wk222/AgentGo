package officedoc

import (
	"fmt"
	"strings"
)

// slide is one finished slide, ready for packaging.
type slide struct {
	xml    string
	layout int
	rels   []slideRel // rId1 is always the layout; these start at rId2
	notes  string
	title  string
}

type slideRel struct{ id, typ, target string }

const (
	relChart = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart"
	relImage = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
)

// sb builds the shape tree of one slide.
type sb struct {
	d      *deck
	shapes strings.Builder
	next   int
	rels   []slideRel
	bg     string // RRGGBB for a full-bleed background; "" = the master's
	bgXML  string // a complete <p:bg> element; wins over bg
	layout int
	title  string
	notes  string
}

func (d *deck) newSlide(layout int) *sb { return &sb{d: d, next: 1, layout: layout} }

func (s *sb) id() int {
	s.next++
	return s.next
}

func (s *sb) addRel(typ, target string) string {
	id := fmt.Sprintf("rId%d", len(s.rels)+2)
	s.rels = append(s.rels, slideRel{id: id, typ: typ, target: target})
	return id
}

func (s *sb) finish() *slide {
	if s.d != nil && s.d.tpl != nil {
		s.slideNumber(s.d.tpl.layouts[s.layout])
	}
	bg := s.bgXML
	if bg == "" && s.bg != "" {
		bg = `<p:bg><p:bgPr><a:solidFill><a:srgbClr val="` + s.bg + `"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>`
	}
	xml := xmlHeader + `<p:sld ` + nsA + ` ` + nsR + ` ` + nsP + `><p:cSld>` + bg + `<p:spTree>` + spTreeHead +
		s.shapes.String() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
	return &slide{xml: xml, layout: s.layout, rels: s.rels, notes: s.notes, title: s.title}
}

func fillXML(color string, alphaPct int) string {
	if color == "" {
		return `<a:noFill/>`
	}
	if alphaPct > 0 && alphaPct < 100 {
		return fmt.Sprintf(`<a:solidFill><a:srgbClr val="%s"><a:alpha val="%d"/></a:srgbClr></a:solidFill>`, color, alphaPct*1000)
	}
	return `<a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill>`
}

func lineXML(color string, widthPt float64) string {
	if color == "" {
		return `<a:ln><a:noFill/></a:ln>`
	}
	return fmt.Sprintf(`<a:ln w="%d"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:ln>`, int(widthPt*12700), color)
}

// boxOpts styles one shape.
type boxOpts struct {
	name   string
	fill   string // RRGGBB; empty = no fill
	alpha  int    // fill opacity in percent; 0 = opaque
	line   string // RRGGBB outline; empty = none
	prst   string // preset geometry; default rect
	anchor string // t, ctr, b
	insetX float64
	insetY float64
	ph     string // placeholder attributes, e.g. `type="title"`
	descr  string // alt text
}

// box adds a shape with optional text. A box with no paragraphs and no
// placeholder is a plain decorative shape.
func (s *sb) box(x, y, w, h float64, ps []para, o boxOpts) {
	if o.prst == "" {
		o.prst = "rect"
	}
	if o.anchor == "" {
		o.anchor = "t"
	}
	if o.insetX == 0 {
		o.insetX = 0.1
	}
	if o.insetY == 0 {
		o.insetY = 0.05
	}
	id := s.id()
	name := o.name
	if name == "" {
		name = fmt.Sprintf("Shape %d", id)
	}
	descr := ""
	if o.descr != "" {
		descr = ` descr="` + escapeXML(o.descr) + `"`
	}
	cnv := `<p:cNvSpPr/>`
	switch {
	case o.ph != "":
		cnv = `<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`
	case len(ps) > 0 && o.fill == "" && o.line == "":
		cnv = `<p:cNvSpPr txBox="1"/>`
	}
	nvpr := `<p:nvPr/>`
	if o.ph != "" {
		nvpr = `<p:nvPr><p:ph ` + o.ph + `/></p:nvPr>`
	}
	fmt.Fprintf(&s.shapes, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"%s/>%s%s</p:nvSpPr><p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom>%s%s</p:spPr>`,
		id, escapeXML(name), descr, cnv, nvpr, xfrmXML(x, y, w, h), o.prst, fillXML(o.fill, o.alpha), lineXML(o.line, 0.75))
	if len(ps) > 0 || o.ph != "" {
		s.shapes.WriteString(fmt.Sprintf(`<p:txBody><a:bodyPr wrap="square" lIns="%d" tIns="%d" rIns="%d" bIns="%d" rtlCol="0" anchor="%s"><a:noAutofit/></a:bodyPr><a:lstStyle/>`,
			emu(o.insetX), emu(o.insetY), emu(o.insetX), emu(o.insetY), o.anchor))
		if len(ps) == 0 {
			s.shapes.WriteString(`<a:p><a:endParaRPr lang="zh-CN"/></a:p>`)
		}
		for _, p := range ps {
			s.shapes.WriteString(s.d.paraXML(p, s.d.pal.Accent))
		}
		s.shapes.WriteString(`</p:txBody>`)
	}
	s.shapes.WriteString(`</p:sp>`)
}

// deco adds a decorative shape (no text).
func (s *sb) deco(name string, x, y, w, h float64, fill string, alpha int, prst string) {
	s.box(x, y, w, h, nil, boxOpts{name: name, fill: fill, alpha: alpha, prst: prst})
}

var titleSizes = []float64{30, 28, 26, 24, 22, 20}

// titlePH adds the slide title as the title placeholder, with a rule under it.
func (s *sb) titlePH(text string) {
	d := s.d
	tb := textBlock{widthIn: contentW, heightIn: titleH, insetX: 0.05, insetY: 0.03}
	ps, size, ok := fitParas(tb, titleSizes, func(sz float64) []para {
		return []para{{text: text, size: sz, bold: true, color: d.pal.Primary, spacing: 0.9}}
	})
	if !ok {
		d.warn("slide title %q is too long for two lines at %.0f pt; shorten it", truncateRunes(text, 24), size)
	}
	s.title = text
	s.box(marginX, titleY, contentW, titleH, ps, boxOpts{name: "Title", ph: `type="title"`, anchor: "b", insetX: 0.05, insetY: 0.03})
	s.deco("Deco rule", marginX+0.05, titleY+titleH+0.05, 0.9, 0.055, d.pal.Accent, 0, "rect")
}

// footer adds the footer text, a hairline and the page number.
func (s *sb) footer() {
	d := s.d
	s.deco("Deco footer line", marginX, 6.93, contentW, 0.012, d.pal.Line, 0, "rect")
	if d.footer != "" {
		s.box(marginX, 6.98, 9.0, 0.3, []para{{text: d.footer, size: 10, color: d.pal.Muted}},
			boxOpts{name: "Footer", anchor: "ctr", insetX: 0.05, insetY: 0.02})
	}
	if d.numbers {
		s.box(slideW-marginX-1.2, 6.98, 1.2, 0.3, []para{{size: 10, color: d.pal.Muted, align: "r", field: "slidenum"}},
			boxOpts{name: "Slide Number", anchor: "ctr", insetX: 0.05, insetY: 0.02})
	}
}

// contentSlide starts a standard slide: title, rule and footer.
func (d *deck) contentSlide(layout int, title, notes string) *sb {
	if d.tpl != nil {
		return d.templateSlide(title, notes)
	}
	s := d.newSlide(layout)
	s.notes = notes
	s.titlePH(title)
	s.footer()
	return s
}

func (d *deck) warn(format string, args ...any) {
	d.warnings = append(d.warnings, fmt.Sprintf("slide %d: ", d.cur)+fmt.Sprintf(format, args...))
}
