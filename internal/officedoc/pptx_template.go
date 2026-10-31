package officedoc

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Template mode: create_pptx can build on the user's own .pptx (a company
// template). Its slide masters, layouts, theme, logos and background art are
// kept as they are; its slides are dropped; the new slides sit on the layouts
// the caller picks. This file reads a template and describes its layouts; the
// package assembly is in pptx_template_pack.go.

const maxTemplatePart = 60 << 20

var (
	layoutPartRE = regexp.MustCompile(`^ppt/slideLayouts/slideLayout(\d+)\.xml$`)
	masterPartRE = regexp.MustCompile(`^ppt/slideMasters/slideMaster(\d+)\.xml$`)
)

// --- XML shapes of masters and layouts (namespace-agnostic) ---

type txSp struct {
	NV struct {
		C struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
		Ph *struct {
			Type string `xml:"type,attr"`
			Idx  *int   `xml:"idx,attr"`
		} `xml:"nvPr>ph"`
	} `xml:"nvSpPr"`
	SpPr struct {
		Xfrm *xXfrm `xml:"xfrm"`
	} `xml:"spPr"`
	// What the placeholder sets for its own text: size, and colour (the template's brand colour for titles).
	Def struct {
		Sz   int `xml:"sz,attr"`
		Fill struct {
			Srgb struct {
				Val string `xml:"val,attr"`
			} `xml:"srgbClr"`
			Scheme struct {
				Val string `xml:"val,attr"`
			} `xml:"schemeClr"`
		} `xml:"solidFill"`
	} `xml:"txBody>lstStyle>lvl1pPr>defRPr"`
}

type txPic struct {
	NV struct {
		C struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
	} `xml:"nvPicPr"`
	SpPr struct {
		Xfrm *xXfrm `xml:"xfrm"`
	} `xml:"spPr"`
}

type txGrp struct {
	NV struct {
		C struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
	} `xml:"nvGrpSpPr"`
	Xfrm *xXfrm `xml:"grpSpPr>xfrm"`
}

type txDoc struct {
	Type         string `xml:"type,attr"`
	ShowMasterSp string `xml:"showMasterSp,attr"`
	CSld         struct {
		Name string `xml:"name,attr"`
		Tree struct {
			Shapes []txSp  `xml:"sp"`
			Pics   []txPic `xml:"pic"`
			Groups []txGrp `xml:"grpSp"`
		} `xml:"spTree"`
	} `xml:"cSld"`
}

// --- the model ---

// tplPH is one placeholder of a layout; positions are inches.
type tplPH struct {
	Type       string // the type attribute as written; "" is an object placeholder
	Idx        int
	HasIdx     bool
	X, Y, W, H float64
	Geo        bool
	Styled     bool // the layout gives this placeholder's text a colour of its own (stock layouts set only a size)
}

// tplDecor is a non-placeholder shape of a layout or its master (logo,
// background art, bars). Positions are inches, clipped to the slide.
type tplDecor struct {
	Kind, Name string
	X, Y, W, H float64
	Full       bool // covers (almost) the whole slide
	Master     bool
}

type tplLayout struct {
	N      int // the number in slideLayoutN.xml
	Name   string
	Master int
	Kind   string // the layout's type attribute (title, obj, titleOnly, ...)
	PH     []tplPH
	Decor  []tplDecor
	Used   []int // sample slides (1-based) of the template that sit on it
	clr    string
}

type template struct {
	rel     string
	parts   map[string][]byte // every part of the package
	order   []string
	w, h    float64 // slide size, inches
	sorted  []*tplLayout
	layouts map[int]*tplLayout
	samples []int // layout number of each sample slide in the template
	cover   *tplLayout
	content *tplLayout
	brand   string // RRGGBB of the template's title colour, if it has one
}

func (l *tplLayout) first(match func(tplPH) bool) *tplPH {
	for i := range l.PH {
		if match(l.PH[i]) {
			return &l.PH[i]
		}
	}
	return nil
}

func (l *tplLayout) titlePH() *tplPH {
	return l.first(func(p tplPH) bool { return p.Type == "title" || p.Type == "ctrTitle" })
}

func (l *tplLayout) numberPH() *tplPH {
	return l.first(func(p tplPH) bool { return p.Type == "sldNum" })
}

// subtitlePH is the placeholder under the title on a cover: the subtitle, or
// failing that the first text body placeholder.
func (l *tplLayout) subtitlePH() *tplPH {
	if p := l.first(func(p tplPH) bool { return p.Type == "subTitle" }); p != nil {
		return p
	}
	var best *tplPH
	for i := range l.PH {
		p := &l.PH[i]
		if p.Type != "" && p.Type != "body" && p.Type != "obj" {
			continue
		}
		if best == nil || p.Idx < best.Idx {
			best = p
		}
	}
	return best
}

// contentBox is where content may go on a slide with this layout, in inches.
// A large body placeholder defines it; otherwise it is the page below the
// title, inside side margins of at least half an inch.
func (l *tplLayout) contentBox(sw, sh float64) area {
	for i := range l.PH {
		p := l.PH[i]
		if p.Type != "" && p.Type != "body" && p.Type != "obj" || !p.Geo || p.Idx == 0 && p.HasIdx {
			continue
		}
		if p.W*p.H >= 0.3*sw*sh {
			return area{p.X, p.Y, p.W, p.H}
		}
	}
	x, y := 0.6, 0.6
	if t := l.titlePH(); t != nil && t.Geo {
		x, y = max(t.X, 0.5), t.Y+t.H+0.25
	}
	bottom := sh - 0.4
	return area{x: x, y: y, w: sw - 2*x, h: max(bottom-y, 1)}
}

// --- loading ---

// loadTemplate reads a .pptx/.potx from the workspace, or a template compiled
// into AgentGo ("builtin:<name>").
func loadTemplate(env Env, relPath string) (*template, error) {
	rel := strings.TrimSpace(relPath)
	if isBuiltinRef(rel) {
		zr, name, err := openBuiltin(rel)
		if err != nil {
			return nil, err
		}
		return templateFromZip(zr.File, builtinPrefix+name, "built-in template "+name)
	}
	if ext := strings.ToLower(filepath.Ext(rel)); ext != ".pptx" && ext != ".potx" {
		return nil, errors.New("template must be a .pptx or .potx file (save .ppt as .pptx first)")
	}
	abs, err := env.Resolve(rel)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot open template %s: %w", filepath.Base(abs), err)
	}
	defer zr.Close()
	return templateFromZip(zr.File, filepath.ToSlash(rel), filepath.Base(abs))
}

// templateFromZip reads the parts of a template package; label names the file in messages.
func templateFromZip(files []*zip.File, rel, label string) (*template, error) {
	t := &template{rel: rel, parts: map[string][]byte{}}
	xmlParts := map[string][]byte{}
	var total uint64
	for _, f := range files {
		name := f.Name
		if strings.HasSuffix(name, "/") {
			continue
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("template %s has an unsafe part name %q", label, name)
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > maxTemplatePart || total > maxPptxBytes {
			return nil, fmt.Errorf("template %s is too large", label)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxTemplatePart+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		t.parts[name] = b
		t.order = append(t.order, name)
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			xmlParts[name] = b
		}
	}
	if _, ok := t.parts["ppt/presentation.xml"]; !ok {
		return nil, errors.New("template is not a PowerPoint file: ppt/presentation.xml is missing")
	}
	if err := t.analyze(&pptxPackage{parts: xmlParts}); err != nil {
		return nil, err
	}
	return t, nil
}

func boxOf(x *xXfrm) (b [4]float64, ok bool) {
	px, py, pw, ph, ok := x.rect()
	return [4]float64{px, py, pw, ph}, ok
}

// decorOf lists the non-placeholder shapes of one tree, clipped to the slide.
func decorOf(doc txDoc, sw, sh float64, master bool) []tplDecor {
	var out []tplDecor
	add := func(kind, name string, x *xXfrm) {
		b, ok := boxOf(x)
		if !ok {
			return
		}
		x0, y0 := max(b[0], 0), max(b[1], 0)
		x1, y1 := min(b[0]+b[2], sw), min(b[1]+b[3], sh)
		if x1-x0 < 0.05 || y1-y0 < 0.05 {
			return
		}
		out = append(out, tplDecor{Kind: kind, Name: name, X: x0, Y: y0, W: x1 - x0, H: y1 - y0,
			Full: (x1-x0) >= 0.85*sw && (y1-y0) >= 0.85*sh, Master: master})
	}
	for _, p := range doc.CSld.Tree.Pics {
		add("picture", p.NV.C.Name, p.SpPr.Xfrm)
	}
	for _, g := range doc.CSld.Tree.Groups {
		add("group", g.NV.C.Name, g.Xfrm)
	}
	for _, s := range doc.CSld.Tree.Shapes {
		if s.NV.Ph == nil {
			add("shape", s.NV.C.Name, s.SpPr.Xfrm)
		}
	}
	return out
}

func phKey(typ string) string {
	switch typ {
	case "ctrTitle":
		return "title"
	case "", "obj", "tbl", "chart", "pic", "media", "dgm", "clipArt", "subTitle":
		return "body"
	}
	return typ
}

// analyze finds the masters, layouts and sample slides.
func (t *template) analyze(pkg *pptxPackage) error {
	t.w, t.h = pkg.slideSize()
	t.layouts = map[int]*tplLayout{}
	masterNo := 0
	for _, r := range pkg.rels("ppt/presentation.xml") {
		if r.Type != relBase+"slideMaster" {
			continue
		}
		masterNo++
		if m := masterPartRE.FindStringSubmatch(r.Target); m != nil { // the number users see: slideMasterN.xml
			masterNo, _ = strconv.Atoi(m[1])
		}
		var md txDoc
		if err := xml.Unmarshal(pkg.parts[r.Target], &md); err != nil {
			return fmt.Errorf("slide master %s cannot be read: %w", r.Target, err)
		}
		mph := map[string][4]float64{}
		for _, s := range md.CSld.Tree.Shapes {
			if s.NV.Ph != nil {
				if b, ok := boxOf(s.SpPr.Xfrm); ok {
					mph[phKey(s.NV.Ph.Type)] = b
				}
			}
		}
		masterDecor := decorOf(md, t.w, t.h, true)
		for _, lr := range pkg.rels(r.Target) {
			if lr.Type != relBase+"slideLayout" {
				continue
			}
			m := layoutPartRE.FindStringSubmatch(lr.Target)
			if m == nil {
				continue
			}
			var ld txDoc
			if err := xml.Unmarshal(pkg.parts[lr.Target], &ld); err != nil {
				return fmt.Errorf("slide layout %s cannot be read: %w", lr.Target, err)
			}
			n, _ := strconv.Atoi(m[1])
			l := &tplLayout{N: n, Name: strings.TrimSpace(ld.CSld.Name), Master: masterNo, Kind: ld.Type}
			for _, s := range ld.CSld.Tree.Shapes {
				if s.NV.Ph == nil {
					continue
				}
				ph := tplPH{Type: s.NV.Ph.Type}
				if s.NV.Ph.Idx != nil {
					ph.Idx, ph.HasIdx = *s.NV.Ph.Idx, true
				}
				b, ok := boxOf(s.SpPr.Xfrm)
				if !ok {
					b, ok = mph[phKey(ph.Type)]
				}
				if ok {
					ph.X, ph.Y, ph.W, ph.H, ph.Geo = b[0], b[1], b[2], b[3], true
				}
				ph.Styled = s.Def.Fill.Srgb.Val != "" || s.Def.Fill.Scheme.Val != ""
				if v := s.Def.Fill.Srgb.Val; (ph.Type == "title" || ph.Type == "ctrTitle") && validBrand(v) {
					l.clr = strings.ToUpper(v)
				}
				l.PH = append(l.PH, ph)
			}
			l.Decor = decorOf(ld, t.w, t.h, false)
			if ld.ShowMasterSp != "0" {
				l.Decor = append(l.Decor, masterDecor...)
			}
			t.layouts[n] = l
			t.sorted = append(t.sorted, l)
		}
	}
	if len(t.sorted) == 0 {
		return errors.New("the template has no slide layouts")
	}
	sort.Slice(t.sorted, func(i, j int) bool {
		a, b := t.sorted[i], t.sorted[j]
		if a.Master != b.Master {
			return a.Master < b.Master
		}
		return a.N < b.N
	})
	for i, part := range pkg.slideOrder() {
		n := 0
		for _, r := range pkg.rels(part) {
			if m := layoutPartRE.FindStringSubmatch(r.Target); m != nil && r.Type == relBase+"slideLayout" {
				n, _ = strconv.Atoi(m[1])
			}
		}
		t.samples = append(t.samples, n)
		if l := t.layouts[n]; l != nil {
			l.Used = append(l.Used, i+1)
		}
	}
	t.chooseDefaults()
	return nil
}

// chooseDefaults picks the cover and content layouts. A template that comes
// with sample slides says what it wants: its first slide is the cover and the
// layout most of the middle slides use is the content layout. Without
// samples, the layouts' own types decide.
func (t *template) chooseDefaults() {
	withTitle := func(l *tplLayout) bool { return l != nil && l.titlePH() != nil }
	if n := len(t.samples); n >= 2 {
		if l := t.layouts[t.samples[0]]; l != nil {
			t.cover = l
		}
		mid := t.samples[1:]
		if n >= 3 {
			mid = t.samples[1 : n-1]
		}
		count := map[int]int{}
		for _, ln := range mid {
			if withTitle(t.layouts[ln]) {
				count[ln]++
			}
		}
		best := 0
		for ln, c := range count {
			if best == 0 || c > count[best] || c == count[best] && ln < best {
				best = ln
			}
		}
		if best != 0 {
			t.content = t.layouts[best]
		}
	}
	pickKind := func(kinds ...string) *tplLayout {
		for _, k := range kinds {
			for _, l := range t.sorted {
				if l.Kind == k && withTitle(l) {
					return l
				}
			}
		}
		return nil
	}
	if t.cover == nil {
		t.cover = pickKind("title")
	}
	if t.content == nil {
		t.content = pickKind("titleOnly", "obj")
	}
	if t.content == nil {
		for _, l := range t.sorted {
			if withTitle(l) && l.titlePH().Type == "title" {
				t.content = l
				break
			}
		}
	}
	if t.cover == nil {
		t.cover = t.content
	}
	if t.content != nil {
		t.brand = t.content.clr
	}
	if t.brand == "" && t.cover != nil {
		t.brand = t.cover.clr
	}
}

// pick resolves a layout given by number ("67", "L67") or name; empty means def.
func (t *template) pick(spec string, def *tplLayout) (*tplLayout, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		if def == nil {
			return nil, errors.New("the template has no layout I can use by default; name one with template_layout (see inspect_pptx layouts)")
		}
		return def, nil
	}
	num := strings.TrimLeft(strings.TrimPrefix(strings.ToLower(spec), "layout"), "l")
	if n, err := strconv.Atoi(num); err == nil {
		if l := t.layouts[n]; l != nil {
			return l, nil
		}
		return nil, fmt.Errorf("template has no layout number %d", n)
	}
	var found []*tplLayout
	for _, l := range t.sorted {
		if l.Name == spec {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("template has no layout named %q (inspect_pptx with layouts=true lists them)", spec)
	case 1:
		return found[0], nil
	}
	// Several masters often carry the same layout name; prefer one the sample slides use.
	var used []*tplLayout
	for _, l := range found {
		if len(l.Used) > 0 {
			used = append(used, l)
		}
	}
	if len(used) == 1 {
		return used[0], nil
	}
	ids := make([]string, len(found))
	for i, l := range found {
		ids[i] = strconv.Itoa(l.N)
	}
	return nil, fmt.Errorf("layout name %q is used by several layouts (numbers %s); use the number", spec, strings.Join(ids, ", "))
}

// --- description for inspect_pptx ---

type tplLayoutInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Master       int      `json:"master"`
	DefaultFor   string   `json:"default_for,omitempty"`
	UsedBy       []int    `json:"used_by_slides,omitempty"`
	Placeholders []string `json:"placeholders,omitempty"`
	Decor        []string `json:"decor,omitempty"`
	ContentBox   string   `json:"content_box_px,omitempty"`
}

type tplInfo struct {
	Canvas string          `json:"svg_canvas"`
	Brand  string          `json:"brand_color,omitempty"`
	Layout []tplLayoutInfo `json:"layouts"`
	Others []string        `json:"other_layouts,omitempty"`
}

const maxOtherLayouts = 40

func (t *template) describe() tplInfo {
	px := 1280 / t.w
	r := func(v float64) int { return int(math.Round(v * px)) }
	info := tplInfo{Canvas: fmt.Sprintf("a svg slide uses viewBox 0 0 1280 %d; the numbers below are in those px", r(t.h)), Brand: t.brand}
	for _, l := range t.sorted {
		role := ""
		switch {
		case l == t.cover && l == t.content:
			role = "cover, content"
		case l == t.cover:
			role = "cover, closing"
		case l == t.content:
			role = "content"
		}
		if role == "" && len(l.Used) == 0 {
			if len(info.Others) < maxOtherLayouts {
				info.Others = append(info.Others, fmt.Sprintf("%d %q ph=[%s]", l.N, l.Name, strings.Join(phTypes(l), ",")))
			}
			continue
		}
		li := tplLayoutInfo{ID: strconv.Itoa(l.N), Name: l.Name, Master: l.Master, DefaultFor: role, UsedBy: l.Used}
		for _, p := range l.PH {
			name := p.Type
			if name == "" {
				name = "object"
			}
			if p.HasIdx {
				name += "#" + strconv.Itoa(p.Idx)
			}
			if p.Geo {
				name += fmt.Sprintf(" x=%d y=%d w=%d h=%d", r(p.X), r(p.Y), r(p.W), r(p.H))
			}
			li.Placeholders = append(li.Placeholders, name)
		}
		for i, d := range l.Decor {
			if i == 12 {
				li.Decor = append(li.Decor, fmt.Sprintf("... %d more", len(l.Decor)-i))
				break
			}
			if d.Full {
				li.Decor = append(li.Decor, fmt.Sprintf("full-slide %s %q (background art)", d.Kind, d.Name))
			} else {
				li.Decor = append(li.Decor, fmt.Sprintf("%s %q x=%d y=%d w=%d h=%d (keep clear)", d.Kind, d.Name, r(d.X), r(d.Y), r(d.W), r(d.H)))
			}
		}
		cb := l.contentBox(t.w, t.h)
		li.ContentBox = fmt.Sprintf("x=%d y=%d w=%d h=%d", r(cb.x), r(cb.y), r(cb.w), r(cb.h))
		info.Layout = append(info.Layout, li)
	}
	return info
}

func phTypes(l *tplLayout) []string {
	var out []string
	for _, p := range l.PH {
		switch p.Type {
		case "dt", "ftr", "sldNum":
			continue
		case "":
			out = append(out, "object")
		default:
			out = append(out, p.Type)
		}
	}
	return out
}

// --- brand colour ---

func validBrand(v string) bool {
	if len(v) != 6 {
		return false
	}
	_, err := strconv.ParseUint(v, 16, 32)
	if err != nil {
		return false
	}
	r, g, b := hexRGB(v)
	return 0.299*r+0.587*g+0.114*b < 0.78*255 // white-on-dark titles say nothing about the brand
}

func hexRGB(v string) (r, g, b float64) {
	n, _ := strconv.ParseUint(v, 16, 32)
	return float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n & 255)
}

// mixHex moves a colour a fraction f of the way to white (toward 255) or black (toward 0).
func mixHex(v string, toward float64, f float64) string {
	r, g, b := hexRGB(v)
	m := func(c float64) int { return int(math.Round(c + (toward-c)*f)) }
	return fmt.Sprintf("%02X%02X%02X", m(r), m(g), m(b))
}

// brandPalette is the blue theme recoloured around the template's brand
// colour, so generated cards, bars and charts match the template.
func brandPalette(brand string) palette {
	p := palettes["blue"]
	p.Secondary = brand
	p.Primary = mixHex(brand, 0, 0.3)
	p.Surface = mixHex(brand, 255, 0.92)
	p.Line = mixHex(brand, 255, 0.8)
	p.Soft = mixHex(brand, 255, 0.7)
	p.Chart = append([]string{brand}, p.Chart[1:]...)
	return p
}

func firstLayout(ls ...*tplLayout) *tplLayout {
	for _, l := range ls {
		if l != nil {
			return l
		}
	}
	return nil
}
