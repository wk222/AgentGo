package officedoc

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Text measuring for slides. Nothing here needs a font file: widths are
// estimated per character class, which is close enough to place text boxes and
// to choose a font size that fits. The estimate errs on the wide side, so a
// box that "fits" has room to spare in PowerPoint and WPS.

const (
	emuPerInch = 914400
	ptPerInch  = 72.0
	// lineFactor is the line height as a multiple of the font size
	// (Microsoft YaHei is tall: about 1.32 at single spacing).
	lineFactor = 1.35
)

func emu(inches float64) int64 { return int64(inches*emuPerInch + 0.5) }

// charEm is the width of r in em (1 em = the font size).
func charEm(r rune) float64 {
	switch {
	case r == ' ':
		return 0.32
	case r == '\t':
		return 1.3
	case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || r >= 0xFF00 || (r >= 0x3000 && r <= 0x303F) || (r >= 0x2010 && r <= 0x2027):
		return 1.0
	case r < 0x80:
		switch {
		case strings.ContainsRune("il.,:;!|'`", r):
			return 0.3
		case strings.ContainsRune("mwMW@%", r):
			return 0.92
		case unicode.IsUpper(r):
			return 0.68
		case unicode.IsDigit(r):
			return 0.58
		case strings.ContainsRune("()[]{}-/\\", r):
			return 0.38
		}
		return 0.56
	}
	return 0.9
}

// countLines estimates how many lines text takes in a box widthEm em wide.
// ASCII words are kept whole; every CJK character may break.
func countLines(text string, widthEm float64) int {
	if widthEm <= 0 {
		return 1
	}
	total := 0
	for _, hard := range strings.Split(text, "\n") {
		total += lineCount(hard, widthEm)
	}
	return max(total, 1)
}

func lineCount(s string, widthEm float64) int {
	lines, cur := 1, 0.0
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		if charEm(r) < 1.0 && r != ' ' && r < 0x80 { // a word of ASCII
			j, w := i, 0.0
			for j < len(rs) && rs[j] != ' ' && rs[j] < 0x80 {
				w += charEm(rs[j])
				j++
			}
			switch {
			case cur+w <= widthEm:
				cur += w
			case w > widthEm: // a word longer than the line breaks anywhere
				if cur > 0 {
					lines++
				}
				lines += int(w / widthEm)
				cur = w - float64(int(w/widthEm))*widthEm
			default:
				lines++
				cur = w
			}
			i = j
			continue
		}
		w := charEm(r)
		if cur+w > widthEm && cur > 0 {
			if r != ' ' {
				lines++
				cur = w
			}
		} else {
			cur += w
		}
		i++
	}
	return lines
}

// para is one paragraph of a text body.
type para struct {
	text    string  // may hold **bold** spans
	size    float64 // pt
	color   string  // RRGGBB; empty = the theme text colour
	bold    bool
	align   string // l, ctr, r
	bullet  bool
	level   int
	before  float64 // space before, pt
	field   string  // "slidenum": the text is replaced by a page-number field
	spacing float64 // line spacing multiple (0 = single)
}

// textBlock is the measuring context of one box.
type textBlock struct {
	widthIn, heightIn float64 // outer size of the shape
	insetX, insetY    float64 // inches, each side
}

// marginFor returns the left indent of a bullet level, in inches.
func marginFor(level int) float64 { return 0.32 + 0.38*float64(level) }

// need returns the height in inches the paragraphs need in the block.
func (tb textBlock) need(ps []para) float64 {
	var pts float64
	for _, p := range ps {
		w := (tb.widthIn - 2*tb.insetX) * ptPerInch
		if p.bullet {
			w -= marginFor(p.level) * ptPerInch
		}
		lines := countLines(stripBold(p.text), w/p.size)
		mult := lineFactor
		if p.spacing > 0 {
			mult *= p.spacing
		}
		pts += float64(lines)*p.size*mult + p.before
	}
	return pts/ptPerInch + 2*tb.insetY
}

func (tb textBlock) fits(ps []para) bool { return tb.need(ps) <= tb.heightIn+0.001 }

// stripBold removes the **bold** markers.
func stripBold(s string) string { return strings.ReplaceAll(s, "**", "") }

// runSpan is a stretch of paragraph text with one weight.
type runSpan struct {
	text string
	bold bool
}

// spans splits **bold** markup; an unbalanced marker stays literal.
func spans(s string, baseBold bool) []runSpan {
	if !strings.Contains(s, "**") || strings.Count(s, "**")%2 != 0 {
		return []runSpan{{text: s, bold: baseBold}}
	}
	var out []runSpan
	for i, part := range strings.Split(s, "**") {
		if part == "" {
			continue
		}
		out = append(out, runSpan{text: part, bold: baseBold || i%2 == 1})
	}
	if len(out) == 0 {
		out = []runSpan{{text: "", bold: baseBold}}
	}
	return out
}

// paraXML renders a paragraph. Everything the paragraph needs is written out
// explicitly, so the look does not depend on the master or on the viewer.
func (d *deck) paraXML(p para, accent string) string {
	var b strings.Builder
	algn := map[string]string{"": "l", "l": "l", "ctr": "ctr", "r": "r"}[p.align]
	if algn == "" {
		algn = "l"
	}
	b.WriteString(`<a:p><a:pPr`)
	if p.bullet {
		fmt.Fprintf(&b, ` marL="%d" indent="%d"`, emu(marginFor(p.level)), -emu(0.26))
		if p.level > 0 { // the outline level, so PowerPoint's outline view shows the structure
			fmt.Fprintf(&b, ` lvl="%d"`, p.level)
		}
	} else {
		b.WriteString(` marL="0" indent="0"`)
	}
	b.WriteString(` algn="` + algn + `">`)
	if p.spacing > 0 {
		fmt.Fprintf(&b, `<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, int(p.spacing*100000))
	}
	fmt.Fprintf(&b, `<a:spcBef><a:spcPts val="%d"/></a:spcBef>`, int(p.before*100))
	if p.bullet {
		ch := "•"
		if p.level > 0 {
			ch = "–"
		}
		b.WriteString(`<a:buClr><a:srgbClr val="` + accent + `"/></a:buClr><a:buFont typeface="Arial"/><a:buChar char="` + ch + `"/>`)
	} else {
		b.WriteString(`<a:buNone/>`)
	}
	b.WriteString(`</a:pPr>`)

	color := p.color
	if color == "" {
		color = d.pal.Text
	}
	rpr := func(bold bool) string {
		attrs := fmt.Sprintf(`lang="zh-CN" altLang="en-US" sz="%d"`, int(p.size*100+0.5))
		if bold {
			attrs += ` b="1"`
		}
		return `<a:rPr ` + attrs + ` dirty="0"><a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill>` + d.fontXML() + `</a:rPr>`
	}
	if p.field == "slidenum" {
		b.WriteString(`<a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum">` + rpr(p.bold) + `<a:t>‹#›</a:t></a:fld>`)
	} else {
		for _, sp := range spans(p.text, p.bold) {
			b.WriteString(`<a:r>` + rpr(sp.bold) + `<a:t>` + escapeXML(sp.text) + `</a:t></a:r>`)
		}
	}
	fmt.Fprintf(&b, `<a:endParaRPr lang="zh-CN" altLang="en-US" sz="%d" dirty="0"/></a:p>`, int(p.size*100+0.5))
	return b.String()
}

func (d *deck) fontXML() string {
	return `<a:latin typeface="` + escapeXML(d.font) + `"/><a:ea typeface="` + escapeXML(d.font) + `"/><a:cs typeface="` + escapeXML(d.font) + `"/>`
}

// fitParas returns the largest size from sizes (descending) at which build(size)
// fits the block, and whether any did. With none fitting it returns the smallest.
func fitParas(tb textBlock, sizes []float64, build func(size float64) []para) ([]para, float64, bool) {
	var last []para
	var lastSize float64
	for _, s := range sizes {
		ps := build(s)
		if tb.fits(ps) {
			return ps, s, true
		}
		last, lastSize = ps, s
	}
	return last, lastSize, false
}

// cellText renders a table cell value (JSON numbers arrive as float64).
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "是"
		}
		return "否"
	}
	return fmt.Sprint(v)
}
