package officedoc

import (
	"fmt"
	"strconv"
	"strings"
)

// Slides in template mode. The title, subtitle and page number are the
// layout's own placeholders with no position or formatting of their own, so
// the template's fonts, colours and places apply; everything else is drawn
// inside the layout's content area.

// phStyle is explicit text formatting for a placeholder whose layout has none.
type phStyle struct {
	size  float64
	bold  bool
	color string
	font  string
}

func (st *phStyle) rPr() string {
	if st == nil {
		return `<a:rPr lang="zh-CN" altLang="en-US" dirty="0"/>`
	}
	attrs := fmt.Sprintf(`lang="zh-CN" altLang="en-US" sz="%d"`, int(st.size*100+0.5))
	if st.bold {
		attrs += ` b="1"`
	}
	f := escapeXML(st.font)
	return `<a:rPr ` + attrs + ` dirty="0"><a:solidFill><a:srgbClr val="` + st.color + `"/></a:solidFill>` +
		`<a:latin typeface="` + f + `"/><a:ea typeface="` + f + `"/><a:cs typeface="` + f + `"/></a:rPr>`
}

// phText adds a placeholder shape that inherits position and formatting from
// the layout; style, when set, formats the text explicitly.
func (s *sb) phText(ph tplPH, name string, lines []string, style *phStyle) {
	attrs := ""
	if ph.Type != "" {
		attrs += ` type="` + ph.Type + `"`
	}
	if ph.HasIdx {
		attrs += ` idx="` + strconv.Itoa(ph.Idx) + `"`
	}
	var ps strings.Builder
	for _, ln := range lines {
		ps.WriteString(`<a:p><a:r>` + style.rPr() + `<a:t>` + escapeXML(ln) + `</a:t></a:r></a:p>`)
	}
	id := s.id()
	fmt.Fprintf(&s.shapes, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph%s/></p:nvPr></p:nvSpPr>`+
		`<p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>%s</p:txBody></p:sp>`, id, name, id, attrs, ps.String())
}

// slideNumber adds the page number if the layout has a place for it.
func (s *sb) slideNumber(l *tplLayout) {
	p := l.numberPH()
	if p == nil {
		return
	}
	idx := ""
	if p.HasIdx {
		idx = ` idx="` + strconv.Itoa(p.Idx) + `"`
	}
	id := s.id()
	fmt.Fprintf(&s.shapes, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Slide Number Placeholder %d"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`+
		`<p:nvPr><p:ph type="sldNum" sz="quarter"%s/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>`+
		`<a:p><a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum"><a:rPr lang="zh-CN" altLang="en-US" dirty="0"/><a:t>‹#›</a:t></a:fld>`+
		`<a:endParaRPr lang="zh-CN" altLang="en-US" dirty="0"/></a:p></p:txBody></p:sp>`, id, id, idx)
}

// useTemplateLayout chooses the layout of the slide about to be built and
// points the content frame at its content area.
func (d *deck) useTemplateLayout(in pptxSlideIn, kind string) error {
	def := d.tplContent
	if kind == "section" {
		def = d.tplCover
	}
	l, err := d.tpl.pick(in.TemplateLayout, def)
	if err != nil {
		return err
	}
	d.curTpl = l
	d.fr = l.contentBox(d.tpl.w, d.tpl.h)
	if kind != "svg" && kind != "section" && l.titlePH() == nil {
		return fmt.Errorf("layout %q (number %d) has no title placeholder; choose another with template_layout, or use the svg layout", l.Name, l.N)
	}
	return nil
}

// templateSlide starts a content slide on the current template layout.
func (d *deck) templateSlide(title, notes string) *sb {
	l := d.curTpl
	s := d.newSlide(l.N)
	s.notes = notes
	s.title = title
	if tp := l.titlePH(); tp != nil {
		d.checkTemplateTitle(title, tp)
		s.phText(*tp, "Title", []string{title}, nil)
	}
	return s
}

// checkTemplateTitle warns about titles that are likely to run into the logo
// that templates keep in a top corner. The layout's title size is not known
// here, so 24 pt is assumed.
func (d *deck) checkTemplateTitle(title string, ph *tplPH) {
	var em float64
	for _, r := range title {
		em += charEm(r)
	}
	limit := 0.62 * d.tpl.w
	if ph.Geo {
		limit = min(limit, ph.W)
	}
	if em*24/ptPerInch > limit {
		d.warn("the title %q is long for this template's title area; shorten it so it does not run into the logo", truncateRunes(title, 24))
	}
}

// tplHero is a cover, section or closing slide: the layout's title, and the
// lines under it in its subtitle placeholder.
func (d *deck) tplHero(l *tplLayout, title string, lines []string, notes string) *slide {
	s := d.newSlide(l.N)
	s.notes, s.title = notes, title
	if tp := l.titlePH(); tp != nil {
		s.phText(*tp, "Title", []string{title}, d.heroTitleStyle(tp, title))
	} else {
		d.warn("layout %q (number %d) has no title placeholder, so the title is drawn as plain text", l.Name, l.N)
		s.box(1, d.tpl.h*0.35, d.tpl.w-2, 1.4, []para{{text: title, size: 40, bold: true, color: d.pal.Secondary, align: "ctr"}},
			boxOpts{name: "Title", anchor: "ctr"})
	}
	if len(lines) > 0 {
		if sp := l.subtitlePH(); sp != nil {
			var st *phStyle
			if !sp.Styled {
				st = &phStyle{size: 20, bold: true, color: d.pal.Secondary, font: d.font}
			}
			s.phText(*sp, "Subtitle", lines, st)
		} else {
			d.warn("layout %q (number %d) has no subtitle placeholder, so %q is left out", l.Name, l.N, strings.Join(lines, " / "))
		}
	}
	return s.finish()
}

// heroTitleStyle formats a cover title when its layout leaves the text plain
// (a plain default title would come out black and small on a branded cover): bold, in the
// brand colour, as large as fits on one line.
func (d *deck) heroTitleStyle(ph *tplPH, title string) *phStyle {
	if ph.Styled {
		return nil
	}
	var em float64
	for _, r := range title {
		em += charEm(r)
	}
	width := 10.0
	if ph.Geo {
		width = ph.W - 0.3
	}
	size := 32.0
	for _, sz := range []float64{54, 48, 44, 40, 36} {
		if em*sz/ptPerInch <= width {
			size = sz
			break
		}
	}
	return &phStyle{size: size, bold: true, color: d.pal.Secondary, font: d.font}
}

// svgDropTitles reconciles an svg slide's id="title" text with the template:
// where the layout has a title placeholder the slide title fills it and the
// text is dropped, otherwise the text stays as an ordinary text box.
func (d *deck) svgDropTitles(sc *svgScene, l *tplLayout) {
	hasTitle := l.titlePH() != nil
	kept := sc.nodes[:0]
	for _, nd := range sc.nodes {
		if nd.title {
			if hasTitle {
				d.warn(`the svg text id="title" was dropped: with a template the slide title fills the layout's title placeholder; leave the heading out of the svg`)
				continue
			}
			nd.title = false
		}
		kept = append(kept, nd)
	}
	sc.nodes = kept
}

// svgOverlaps warns where an svg shape covers part of the template's art
// (logo, bars), which the slide cannot move.
func (d *deck) svgOverlaps(sc *svgScene, l *tplLayout) {
	for _, dc := range l.Decor {
		if dc.Full || dc.W*dc.H < 0.1 {
			continue
		}
		for _, nd := range sc.nodes {
			ix := min(nd.x+nd.w, dc.X+dc.W) - max(nd.x, dc.X)
			iy := min(nd.y+nd.h, dc.Y+dc.H) - max(nd.y, dc.Y)
			if ix > 0.1 && iy > 0.1 && ix*iy > 0.25*dc.W*dc.H {
				d.warn("a %s at about x=%.0f y=%.0f px overlaps the template's %s %q; move it clear (see content_box_px of the layout)",
					nd.kind, nd.x*1280/d.tpl.w, nd.y*1280/d.tpl.w, dc.Kind, dc.Name)
				break
			}
		}
	}
}
