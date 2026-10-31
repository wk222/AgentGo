package officedoc

import (
	"fmt"
	"math"
	"strings"
)

// DrawingML writer: scene nodes -> p:sp / p:cxnSp / p:grpSp / p:pic. Shapes
// are written in absolute slide coordinates; groups use an identity child
// mapping (chOff = off, chExt = ext), so a group moves and resizes as a unit
// without any coordinate rewriting.

// emuR rounds inches to EMU (negative coordinates are legal: shapes may bleed).
func emuR(v float64) int64 { return int64(math.Round(v * emuPerInch)) }

func colorXML(rgb string, alpha float64) string {
	if alpha >= 0.999 {
		return `<a:srgbClr val="` + rgb + `"/>`
	}
	return fmt.Sprintf(`<a:srgbClr val="%s"><a:alpha val="%d"/></a:srgbClr>`, rgb, int(math.Round(math.Max(0, alpha)*100000)))
}

// svgFillXML renders a fill; w and h (any consistent unit) turn a gradient's
// objectBoundingBox direction into a real angle.
func svgFillXML(p svgPaint, w, h float64) string {
	switch {
	case p.none:
		return `<a:noFill/>`
	case p.grad != nil:
		return gradFillXML(p.grad, w, h)
	}
	return `<a:solidFill>` + colorXML(p.rgb, p.alpha) + `</a:solidFill>`
}

func gradFillXML(g *svgGrad, w, h float64) string {
	var b strings.Builder
	b.WriteString(`<a:gradFill rotWithShape="1"><a:gsLst>`)
	for _, s := range g.stops {
		fmt.Fprintf(&b, `<a:gs pos="%d">%s</a:gs>`, int(math.Round(s.pos*100000)), colorXML(s.rgb, s.alpha))
	}
	b.WriteString(`</a:gsLst>`)
	if g.radial {
		l, t := int(math.Round(g.cx*100000)), int(math.Round(g.cy*100000))
		fmt.Fprintf(&b, `<a:path path="circle"><a:fillToRect l="%d" t="%d" r="%d" b="%d"/></a:path>`, l, t, 100000-l, 100000-t)
	} else {
		dx, dy := g.x2-g.x1, g.y2-g.y1
		if !g.userSpace {
			dx, dy = dx*w, dy*h
		}
		deg := math.Atan2(dy, dx) * 180 / math.Pi
		if deg < 0 {
			deg += 360
		}
		fmt.Fprintf(&b, `<a:lin ang="%d" scaled="0"/>`, int(math.Round(deg*60000))%21600000)
	}
	b.WriteString(`</a:gradFill>`)
	return b.String()
}

func svgLineXML(nd *scNode) string {
	if nd.stroke.none || nd.strokeW <= 0 {
		return `<a:ln><a:noFill/></a:ln>`
	}
	cap := map[string]string{"round": "rnd", "square": "sq"}[nd.cap]
	if cap == "" {
		cap = "flat"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<a:ln w="%d" cap="%s"><a:solidFill>%s</a:solidFill>`, int(math.Round(nd.strokeW*12700)), cap, colorXML(nd.stroke.rgb, nd.stroke.alpha))
	if nd.dash != "" {
		b.WriteString(`<a:prstDash val="` + nd.dash + `"/>`)
	}
	switch nd.join {
	case "round":
		b.WriteString(`<a:round/>`)
	case "bevel":
		b.WriteString(`<a:bevel/>`)
	default:
		b.WriteString(`<a:miter lim="800000"/>`)
	}
	if nd.kind == "line" || nd.kind == "path" { // arrowheads only mean something on open strokes
		if nd.arrowStart {
			b.WriteString(`<a:headEnd type="triangle" w="med" len="med"/>`)
		}
		if nd.arrowEnd {
			b.WriteString(`<a:tailEnd type="triangle" w="med" len="med"/>`)
		}
	}
	b.WriteString(`</a:ln>`)
	return b.String()
}

func effectXML(sh *scShadow) string {
	if sh == nil {
		return ""
	}
	return fmt.Sprintf(`<a:effectLst><a:outerShdw blurRad="%d" dist="%d" dir="%d" algn="ctr" rotWithShape="0">%s</a:outerShdw></a:effectLst>`,
		int(math.Round(sh.blurPt*12700)), int(math.Round(sh.distPt*12700)), int(math.Round(sh.dirDeg*60000))%21600000, colorXML(sh.rgb, sh.alpha))
}

func xfrmFor(nd *scNode) string {
	attrs := ""
	if nd.rot != 0 {
		r := math.Mod(nd.rot, 360)
		if r < 0 {
			r += 360
		}
		attrs += fmt.Sprintf(` rot="%d"`, int(math.Round(r*60000))%21600000)
	}
	if nd.flipH {
		attrs += ` flipH="1"`
	}
	if nd.flipV {
		attrs += ` flipV="1"`
	}
	return fmt.Sprintf(`<a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, attrs, emuR(nd.x), emuR(nd.y), emuR(nd.w), emuR(nd.h))
}

// custGeomXML writes a freeform: path coordinates are EMU relative to the
// shape's frame, with the path box equal to the frame.
func custGeomXML(nd *scNode) string {
	var b strings.Builder
	b.WriteString(`<a:custGeom><a:avLst/><a:gdLst/><a:ahLst/><a:cxnLst/><a:rect l="0" t="0" r="r" b="b"/><a:pathLst>`)
	attrs := ""
	if nd.fill.none {
		attrs += ` fill="none"`
	}
	if nd.stroke.none {
		attrs += ` stroke="0"`
	}
	fmt.Fprintf(&b, `<a:path w="%d" h="%d"%s>`, max(emuR(nd.w), 1), max(emuR(nd.h), 1), attrs)
	pt := func(p pt) string {
		return fmt.Sprintf(`<a:pt x="%d" y="%d"/>`, emuR(p.x-nd.x), emuR(p.y-nd.y))
	}
	for _, sg := range nd.segs {
		switch sg.op {
		case 'M':
			b.WriteString(`<a:moveTo>` + pt(sg.p[0]) + `</a:moveTo>`)
		case 'L':
			b.WriteString(`<a:lnTo>` + pt(sg.p[0]) + `</a:lnTo>`)
		case 'C':
			b.WriteString(`<a:cubicBezTo>` + pt(sg.p[0]) + pt(sg.p[1]) + pt(sg.p[2]) + `</a:cubicBezTo>`)
		case 'Z':
			b.WriteString(`<a:close/>`)
		}
	}
	b.WriteString(`</a:path></a:pathLst></a:custGeom>`)
	return b.String()
}

// writeNodes appends nodes to the slide's shape tree. top is true for nodes
// directly under the slide (only those may become the title placeholder).
func (s *sb) writeNodes(nodes []*scNode, top bool) {
	for _, nd := range nodes {
		s.writeNode(nd, top)
	}
}

func (s *sb) writeNode(nd *scNode, top bool) {
	switch nd.kind {
	case "group":
		id := s.id()
		x, y, w, h := emuR(nd.x), emuR(nd.y), max(emuR(nd.w), 1), max(emuR(nd.h), 1)
		fmt.Fprintf(&s.shapes, `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="%s"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`+
			`<p:grpSpPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/><a:chOff x="%d" y="%d"/><a:chExt cx="%d" cy="%d"/></a:xfrm></p:grpSpPr>`,
			id, escapeXML(firstNonEmpty(nd.name, fmt.Sprintf("Group %d", id))), x, y, w, h, x, y, w, h)
		s.writeNodes(nd.kids, false)
		s.shapes.WriteString(`</p:grpSp>`)
	case "rect", "ellipse", "path":
		s.writeShape(nd)
	case "line":
		s.writeLine(nd)
	case "text":
		s.writeText(nd, top)
	case "image":
		s.writePic(nd)
	}
}

func descrAttr(nd *scNode) string {
	if nd.descr == "" {
		return ""
	}
	return ` descr="` + escapeXML(nd.descr) + `"`
}

func (s *sb) writeShape(nd *scNode) {
	id := s.id()
	var geom, label string
	switch nd.kind {
	case "ellipse":
		geom, label = `<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom>`, "Oval"
	case "path":
		geom, label = custGeomXML(nd), "Freeform"
	case "rect":
		label = "Rectangle"
		geom = `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`
		if nd.radius > 0 {
			label = "Rounded Rectangle"
			adj := int(math.Round(math.Min(0.5, nd.radius/math.Min(nd.w, nd.h)) * 100000))
			geom = fmt.Sprintf(`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val %d"/></a:avLst></a:prstGeom>`, adj)
		}
	}
	name := firstNonEmpty(nd.name, fmt.Sprintf("%s %d", label, id))
	fmt.Fprintf(&s.shapes, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"%s/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s%s%s%s%s</p:spPr></p:sp>`,
		id, escapeXML(name), descrAttr(nd), xfrmFor(nd), geom, svgFillXML(nd.fill, nd.w, nd.h), svgLineXML(nd), effectXML(nd.shadow))
}

func (s *sb) writeLine(nd *scNode) {
	id := s.id()
	name := firstNonEmpty(nd.name, fmt.Sprintf("Line %d", id))
	fmt.Fprintf(&s.shapes, `<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="%s"%s/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr><p:spPr>%s<a:prstGeom prst="line"><a:avLst/></a:prstGeom>%s%s</p:spPr></p:cxnSp>`,
		id, escapeXML(name), descrAttr(nd), xfrmFor(nd), svgLineXML(nd), effectXML(nd.shadow))
}

func (s *sb) writePic(nd *scNode) {
	id := s.id()
	name := fmt.Sprintf("image%d%s", len(s.d.images)+1, nd.imgExt)
	s.d.images = append(s.d.images, imagePart{name: name, data: nd.img})
	rid := s.addRel(relImage, "../media/"+name)
	fmt.Fprintf(&s.shapes, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"%s/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`+
		`<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`,
		id, escapeXML(firstNonEmpty(nd.name, fmt.Sprintf("Picture %d", id))), descrAttr(nd), rid, xfrmFor(nd))
}

func (s *sb) writeText(nd *scNode, top bool) {
	id := s.id()
	title := top && nd.title
	cnv, nvpr := `<p:cNvSpPr txBox="1"/>`, `<p:nvPr/>`
	label := "Text"
	if title {
		cnv, nvpr, label = `<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`, `<p:nvPr><p:ph type="title"/></p:nvPr>`, "Title"
	}
	algn := map[string]string{"middle": "ctr", "end": "r"}[nd.anchor]
	if algn == "" {
		algn = "l"
	}
	fmt.Fprintf(&s.shapes, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"%s/>%s%s</p:nvSpPr><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`,
		id, escapeXML(firstNonEmpty(nd.name, fmt.Sprintf("%s %d", label, id))), descrAttr(nd), cnv, nvpr, xfrmFor(nd))
	s.shapes.WriteString(`<p:txBody><a:bodyPr wrap="none" lIns="0" tIns="0" rIns="0" bIns="0" rtlCol="0" anchor="t"><a:noAutofit/></a:bodyPr><a:lstStyle/>`)
	for _, l := range nd.lines {
		fmt.Fprintf(&s.shapes, `<a:p><a:pPr marL="0" indent="0" algn="%s"><a:lnSpc><a:spcPts val="%d"/></a:lnSpc><a:spcBef><a:spcPts val="0"/></a:spcBef><a:buNone/></a:pPr>`,
			algn, max(100, int(math.Round(l.pitch*100))))
		last := 12.0
		for _, r := range l.runs {
			last = r.size
			s.shapes.WriteString(`<a:r>` + s.runPr(r, title) + `<a:t>` + escapeXML(r.text) + `</a:t></a:r>`)
		}
		fmt.Fprintf(&s.shapes, `<a:endParaRPr lang="zh-CN" altLang="en-US" sz="%d" dirty="0"/></a:p>`, fontSz(last))
	}
	s.shapes.WriteString(`</p:txBody></p:sp>`)
}

// fontSz converts points to DrawingML hundredths of a point (1..4000 pt).
func fontSz(pt float64) int { return max(100, min(400000, int(math.Round(pt*100)))) }

func (s *sb) runPr(r scRun, explicitBold bool) string {
	attrs := fmt.Sprintf(`lang="zh-CN" altLang="en-US" sz="%d"`, fontSz(r.size))
	switch {
	case r.bold:
		attrs += ` b="1"`
	case explicitBold: // the title placeholder inherits bold from the master
		attrs += ` b="0"`
	}
	if r.italic {
		attrs += ` i="1"`
	}
	if r.under {
		attrs += ` u="sng"`
	}
	if r.strike {
		attrs += ` strike="sngStrike"`
	}
	if r.spacing != 0 {
		attrs += fmt.Sprintf(` spc="%d"`, int(math.Round(r.spacing*100)))
	}
	fill := `<a:noFill/>`
	if r.rgb != "" && r.alpha > 0 {
		fill = `<a:solidFill>` + colorXML(r.rgb, r.alpha) + `</a:solidFill>`
	}
	font := escapeXML(firstNonEmpty(r.font, s.d.font))
	return `<a:rPr ` + attrs + ` dirty="0">` + fill + `<a:latin typeface="` + font + `"/><a:ea typeface="` + font + `"/><a:cs typeface="` + font + `"/></a:rPr>`
}
