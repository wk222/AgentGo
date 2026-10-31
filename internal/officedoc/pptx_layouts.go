package officedoc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registered for image.DecodeConfig
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
)

type pptxColumnIn struct {
	Heading string   `json:"heading,omitempty" jsonschema:"description=Column heading."`
	Bullets []string `json:"bullets,omitempty" jsonschema:"description=Points\\, same rules as bullets."`
	Text    string   `json:"text,omitempty" jsonschema:"description=A short paragraph instead of points."`
}

type pptxStatIn struct {
	Value string `json:"value" jsonschema:"required,description=The big number or short value\\, e.g. 98.5% or 1.2亿."`
	Label string `json:"label" jsonschema:"required,description=What the value measures."`
	Note  string `json:"note,omitempty" jsonschema:"description=Small comparison or context\\, e.g. 较上月 +0.3pt."`
}

type pptxStepIn struct {
	Title  string `json:"title" jsonschema:"required,description=Step name or date."`
	Detail string `json:"detail,omitempty" jsonschema:"description=One or two short sentences."`
}

// bulletParas turns "- sub point" strings into leveled paragraphs.
func bulletParas(items []string, size float64, pal palette) []para {
	var out []para
	for i, raw := range items {
		text, level := strings.TrimSpace(raw), 0
		if strings.HasPrefix(text, "- ") {
			text, level = strings.TrimSpace(strings.TrimPrefix(text, "- ")), 1
		}
		text = strings.TrimLeft(text, "•· ")
		if text == "" {
			continue
		}
		p := para{text: text, size: size, color: pal.Text, bullet: true, level: level}
		if level == 1 {
			p.size = max(size-2, 11)
		}
		if i > 0 {
			p.before = p.size * 0.5
			if level == 1 {
				p.before = p.size * 0.3
			}
		}
		out = append(out, p)
	}
	return out
}

func (d *deck) bulletsSlide(in pptxSlideIn) (*slide, error) {
	if len(bulletParas(in.Bullets, 20, d.pal)) == 0 {
		return nil, errors.New("bullets is required")
	}
	s := d.contentSlide(layoutContent, in.Title, in.Notes)
	tb := textBlock{widthIn: d.fr.w, heightIn: d.fr.h, insetX: 0.1, insetY: 0.05}
	ps, size, ok := fitParas(tb, []float64{26, 24, 22, 20, 18, 16, 14}, func(sz float64) []para { return bulletParas(in.Bullets, sz, d.pal) })
	if !ok {
		d.warn("these %d points do not fit even at %.0f pt; split the slide or shorten the text", len(ps), size)
	}
	opts := boxOpts{name: "Content", ph: `idx="1"`}
	if d.tpl != nil { // a template's layouts have no body placeholder of ours to fill
		opts.ph = ""
	}
	s.box(d.fr.x, d.fr.y, d.fr.w, d.fr.h, ps, opts)
	return s.finish(), nil
}

func (d *deck) columnParas(c *pptxColumnIn, size float64) []para {
	var ps []para
	if h := strings.TrimSpace(c.Heading); h != "" {
		ps = append(ps, para{text: h, size: size + 2, bold: true, color: d.pal.Secondary})
	}
	if strings.TrimSpace(c.Text) != "" {
		ps = append(ps, para{text: strings.TrimSpace(c.Text), size: size, color: d.pal.Text, before: size * 0.6, spacing: 1.1})
	}
	for _, p := range bulletParas(c.Bullets, size, d.pal) {
		p.before = max(p.before, size*0.5)
		ps = append(ps, p)
	}
	return ps
}

func (d *deck) twoColumnSlide(in pptxSlideIn) (*slide, error) {
	if in.Left == nil || in.Right == nil {
		return nil, errors.New("left and right are required")
	}
	s := d.contentSlide(layoutTitleOnly, in.Title, in.Notes)
	const gap = 0.35
	w, h := (d.fr.w-gap)/2, d.fr.h-0.1
	for i, col := range []*pptxColumnIn{in.Left, in.Right} {
		if len(d.columnParas(col, 16)) == 0 {
			return nil, errors.New("each column needs a heading, text or bullets")
		}
		x := d.fr.x + float64(i)*(w+gap)
		tb := textBlock{widthIn: w, heightIn: h, insetX: 0.3, insetY: 0.25}
		ps, size, ok := fitParas(tb, []float64{24, 22, 20, 18, 16, 14, 12}, func(sz float64) []para { return d.columnParas(col, sz) })
		if !ok {
			d.warn("the %s column does not fit even at %.0f pt; shorten it", []string{"left", "right"}[i], size)
		}
		s.box(x, d.fr.y, w, h, ps, boxOpts{name: []string{"Left column", "Right column"}[i], fill: d.pal.Surface, insetX: 0.3, insetY: 0.25})
		barColor := d.pal.Secondary
		if i == 1 {
			barColor = d.pal.Accent
		}
		s.deco("Deco column bar", x, d.fr.y, w, 0.07, barColor, 0, "rect")
	}
	return s.finish(), nil
}

func (d *deck) statsSlide(in pptxSlideIn) (*slide, error) {
	n := len(in.Stats)
	if n < 2 || n > 6 {
		return nil, fmt.Errorf("stats needs 2 to 6 entries, got %d", n)
	}
	s := d.contentSlide(layoutTitleOnly, in.Title, in.Notes)
	cols, rows := n, 1
	if n > 4 {
		cols, rows = 3, (n+2)/3
	}
	const gap = 0.3
	cw := (d.fr.w - gap*float64(cols-1)) / float64(cols)
	ch := 3.0
	if rows == 2 {
		ch = (d.fr.h - 0.1 - gap) / 2
	}
	top := d.fr.y + (d.fr.h-0.1-(float64(rows)*ch+float64(rows-1)*gap))/2
	for i, st := range in.Stats {
		x := d.fr.x + float64(i%cols)*(cw+gap)
		y := top + float64(i/cols)*(ch+gap)
		tb := textBlock{widthIn: cw, heightIn: ch, insetX: 0.35, insetY: 0.15}
		valueSizes := []float64{48, 44, 40, 36, 32, 28, 24, 20}
		if rows == 2 {
			valueSizes = valueSizes[2:]
		}
		ps, size, ok := fitParas(tb, valueSizes, func(sz float64) []para {
			out := []para{{text: strings.TrimSpace(st.Value), size: sz, bold: true, color: d.pal.Secondary, spacing: 0.95},
				{text: strings.TrimSpace(st.Label), size: 18 - float64(rows-1)*2, bold: true, color: d.pal.Text, before: 6}}
			if note := strings.TrimSpace(st.Note); note != "" {
				out = append(out, para{text: note, size: 13, color: d.pal.Muted, before: 6})
			}
			return out
		})
		if !ok {
			d.warn("stat %d (%q) does not fit even at %.0f pt; shorten it", i+1, st.Value, size)
		}
		s.box(x, y, cw, ch, ps, boxOpts{name: fmt.Sprintf("Stat %d", i+1), fill: d.pal.Surface, anchor: "ctr", insetX: 0.35, insetY: 0.15})
		s.deco("Deco stat bar", x, y, 0.09, ch, d.pal.Secondary, 0, "rect")
	}
	return s.finish(), nil
}

func (d *deck) timelineSlide(in pptxSlideIn) (*slide, error) {
	n := len(in.Steps)
	if n < 2 || n > 6 {
		return nil, fmt.Errorf("steps needs 2 to 6 entries, got %d (split a longer timeline over two slides)", n)
	}
	s := d.contentSlide(layoutTitleOnly, in.Title, in.Notes)
	colW := d.fr.w / float64(n)
	textW := colW - 0.2
	const circle = 0.64

	// Measure first: every column uses the same sizes, and the whole block is
	// centred in the body area from the tallest column.
	titleTB := textBlock{widthIn: textW, heightIn: 1.0, insetX: 0.05, insetY: 0.03}
	detailTB := textBlock{widthIn: textW, heightIn: 2.6, insetX: 0.05, insetY: 0.03}
	titleSize, detailSize := 20.0, 16.0
	for _, st := range in.Steps {
		_, ts, _ := fitParas(titleTB, []float64{20, 18, 16, 14, 12}, func(sz float64) []para {
			return []para{{text: st.Title, size: sz, bold: true, align: "ctr"}}
		})
		titleSize = min(titleSize, ts)
		_, ds, ok := fitParas(detailTB, []float64{16, 15, 14, 13, 12}, func(sz float64) []para {
			return []para{{text: st.Detail, size: sz, align: "ctr", spacing: 1.05}}
		})
		detailSize = min(detailSize, ds)
		if !ok {
			d.warn("the detail of step %q does not fit; shorten it", truncateRunes(st.Title, 12))
		}
	}
	var titleH, detailH float64
	for _, st := range in.Steps {
		titleH = max(titleH, titleTB.need([]para{{text: st.Title, size: titleSize, bold: true, align: "ctr"}}))
		if strings.TrimSpace(st.Detail) != "" {
			detailH = max(detailH, detailTB.need([]para{{text: st.Detail, size: detailSize, align: "ctr", spacing: 1.05}}))
		}
	}
	top := d.fr.y + max(0.1, (d.fr.h-0.1-(circle+0.2+titleH+0.1+detailH))*0.4)
	lineY := top + circle/2
	s.deco("Deco timeline", d.fr.x+colW/2, lineY-0.025, colW*float64(n-1), 0.05, d.pal.Line, 0, "rect")
	for i, st := range in.Steps {
		cx := d.fr.x + colW*(float64(i)+0.5)
		x := d.fr.x + colW*float64(i) + 0.1
		s.box(cx-circle/2, top, circle, circle, []para{{text: fmt.Sprint(i + 1), size: 20, bold: true, color: "FFFFFF", align: "ctr"}},
			boxOpts{name: fmt.Sprintf("Step %d marker", i+1), fill: d.pal.Secondary, prst: "ellipse", anchor: "ctr", insetX: 0.02, insetY: 0.02})
		s.box(x, top+circle+0.2, textW, titleH+0.02,
			[]para{{text: st.Title, size: titleSize, bold: true, color: d.pal.Primary, align: "ctr"}},
			boxOpts{name: fmt.Sprintf("Step %d title", i+1), insetX: 0.05, insetY: 0.03})
		if strings.TrimSpace(st.Detail) != "" {
			s.box(x, top+circle+0.2+titleH+0.1, textW, detailH+0.02,
				[]para{{text: st.Detail, size: detailSize, color: d.pal.Text, align: "ctr", spacing: 1.05}},
				boxOpts{name: fmt.Sprintf("Step %d detail", i+1), insetX: 0.05, insetY: 0.03})
		}
	}
	return s.finish(), nil
}

// heroSlide is the full-bleed slide used for the cover, section dividers and
// the closing slide.
func (d *deck) heroSlide(title, sub, meta, notes string, big bool) *slide {
	s := d.newSlide(layoutTitle)
	s.bg = d.pal.Primary
	s.notes = notes
	s.title = title
	s.deco("Deco circle large", 9.4, -1.6, 6.2, 6.2, d.pal.Secondary, 35, "ellipse")
	s.deco("Deco circle small", 11.3, 4.5, 3.4, 3.4, d.pal.Soft, 18, "ellipse")
	y := 2.55
	if !big {
		y = 2.85
	}
	s.deco("Deco rule", 0.9, y, 1.2, 0.09, d.pal.Accent, 0, "rect")

	tb := textBlock{widthIn: 8.6, heightIn: 1.9, insetX: 0.05, insetY: 0.05}
	sizes := []float64{44, 40, 36, 32, 28, 24}
	if !big {
		sizes = []float64{40, 36, 32, 28, 24}
	}
	ps, size, ok := fitParas(tb, sizes, func(sz float64) []para {
		return []para{{text: title, size: sz, bold: true, color: "FFFFFF", spacing: 0.95}}
	})
	if !ok {
		d.warn("the title %q is too long for this slide at %.0f pt; shorten it", truncateRunes(title, 24), size)
	}
	// The title box is as tall as its text, so the subtitle sits right under it.
	titleBoxH := min(max(tb.need(ps), 0.9)+0.1, tb.heightIn)
	s.box(0.9, y+0.2, 8.6, titleBoxH, ps, boxOpts{name: "Title", ph: `type="ctrTitle"`, anchor: "t", insetX: 0.05, insetY: 0.05})
	if strings.TrimSpace(sub) != "" {
		stb := textBlock{widthIn: 8.6, heightIn: 0.95, insetX: 0.05, insetY: 0.05}
		sps, _, _ := fitParas(stb, []float64{22, 20, 18, 16}, func(sz float64) []para {
			return []para{{text: strings.TrimSpace(sub), size: sz, color: d.pal.Soft, spacing: 1.05}}
		})
		s.box(0.9, y+0.2+titleBoxH+0.2, 8.6, 0.95, sps, boxOpts{name: "Subtitle", ph: `type="subTitle" idx="1"`, insetX: 0.05, insetY: 0.05})
	}
	if strings.TrimSpace(meta) != "" {
		s.box(0.9, 6.35, 8.6, 0.5, []para{{text: strings.TrimSpace(meta), size: 14, color: d.pal.Soft}},
			boxOpts{name: "Meta", anchor: "ctr", insetX: 0.05, insetY: 0.03})
	}
	return s.finish()
}

func (d *deck) sectionSlide(in pptxSlideIn) (*slide, error) {
	if d.tpl != nil {
		return d.tplHero(d.curTpl, in.Title, nonEmpty(in.Body), in.Notes), nil
	}
	return d.heroSlide(in.Title, in.Body, "", in.Notes, false), nil
}

// imagePart is a picture to store in the package.
type imagePart struct {
	name string // file name inside ppt/media
	data []byte
}

const maxImageBytes = 20 << 20

func (d *deck) imageSlide(in pptxSlideIn) (*slide, error) {
	rel := strings.TrimSpace(in.Image)
	if rel == "" {
		return nil, errors.New("image is required")
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return nil, errors.New("image must be a .png or .jpg file")
	}
	abs, err := d.env.Resolve(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", rel, err)
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image %s is too large: %d bytes (max %d)", rel, len(data), maxImageBytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return nil, fmt.Errorf("image %s is not a readable picture", rel)
	}

	s := d.contentSlide(layoutTitleOnly, in.Title, in.Notes)
	areaH := d.fr.h - 0.1
	if strings.TrimSpace(in.Caption) != "" {
		areaH -= 0.5
	}
	scale := min(d.fr.w/float64(cfg.Width), areaH/float64(cfg.Height))
	w, h := float64(cfg.Width)*scale, float64(cfg.Height)*scale
	x, y := d.fr.x+(d.fr.w-w)/2, d.fr.y+(areaH-h)/2

	outExt := ".jpg"
	if ext == ".png" {
		outExt = ".png"
	}
	name := fmt.Sprintf("image%d%s", len(d.images)+1, outExt)
	d.images = append(d.images, imagePart{name: name, data: data})
	rid := s.addRel(relImage, "../media/"+name)
	alt := firstNonEmpty(strings.TrimSpace(in.Caption), in.Title)
	id := s.id()
	fmt.Fprintf(&s.shapes, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="Picture %d" descr="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`+
		`<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`+
		`<p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`,
		id, id, escapeXML(alt), rid, xfrmXML(x, y, w, h))
	if c := strings.TrimSpace(in.Caption); c != "" {
		s.box(d.fr.x, d.fr.y+areaH+0.05, d.fr.w, 0.4, []para{{text: c, size: 13, color: d.pal.Muted, align: "ctr"}},
			boxOpts{name: "Caption", anchor: "ctr", insetX: 0.05, insetY: 0.02})
	}
	return s.finish(), nil
}
