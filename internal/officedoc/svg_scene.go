package officedoc

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the decoder used by image.DecodeConfig
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Scene IR: the one intermediate model between the SVG tree and DrawingML.
// Coordinates are slide inches, strokes and fonts are points; everything is
// already flattened through the transform stack, so the writer never sees a
// matrix.

type scRun struct {
	text                        string
	size                        float64 // user units while converting, points afterwards
	bold, italic, under, strike bool
	rgb                         string
	alpha                       float64
	font                        string
	spacing                     float64 // letter spacing, user units then points
}

type scLine struct {
	x, y  float64 // baseline start (user units while converting)
	runs  []scRun
	pitch float64 // exact line spacing: user units while converting, points afterwards
}

type scShadow struct {
	blurPt, distPt, dirDeg float64
	rgb                    string
	alpha                  float64
}

type scNode struct {
	kind         string // rect, ellipse, line, path, text, image, group
	name, descr  string
	x, y, w, h   float64 // inches
	rot          float64 // degrees clockwise
	flipH, flipV bool
	radius       float64 // inches (rect corners)
	segs         []pathSeg
	fill, stroke svgPaint
	strokeW      float64 // points
	dash         string
	cap, join    string
	arrowStart   bool
	arrowEnd     bool
	shadow       *scShadow

	lines  []scLine
	anchor string // start, middle, end
	title  bool

	img    []byte
	imgExt string

	kids []*scNode
}

type svgScene struct {
	nodes    []*scNode
	bg       string // XML of the slide background, "" when the SVG has none
	warnings []string
}

const (
	maxSVGShapes = 1500
	// Every paragraph gets an exact line pitch P, so a font's own line height
	// does not matter. Where the baseline lands inside such a line box depends on
	// the application: WPS puts it at 0.81*P (measured, Microsoft YaHei), while
	// PowerPoint is believed to keep it one descent (0.25 em) above the bottom,
	// P-0.25*size. The two agree when P is the natural line height (1.31 em) and
	// differ by 0.19*(P-1.31*size) otherwise. We aim at their midpoint,
	// baseline = top + svgFitK*P - svgFitS*size, which halves the worst case: a
	// text block shifts as a whole by at most ~0.1*(P-1.3*size) in either viewer
	// (under 2 px for 32 px text at a 60 px pitch) and line spacing stays exact.
	svgFitK    = 0.9045
	svgFitS    = 0.1265
	svgNatural = 1.31 // natural line height in em, used for a lone line
)

type svgConv struct {
	d        *deck
	ids      map[string]*svgNode
	grads    map[string]*svgGrad
	k        float64 // inches per user unit
	errs     []string
	nerr     int
	warns    []string
	warnSeen map[string]bool
	shapes   int
	title    bool
}

func (c *svgConv) errf(n *svgNode, format string, a ...any) {
	c.nerr++
	if len(c.errs) < 30 {
		c.errs = append(c.errs, n.label()+": "+fmt.Sprintf(format, a...))
	}
}

func (c *svgConv) warnf(n *svgNode, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if c.warnSeen[msg] {
		return
	}
	c.warnSeen[msg] = true
	if len(c.warns) < 12 {
		c.warns = append(c.warns, "svg "+n.label()+": "+msg)
	}
}

const svgAllowed = "svg, g, defs, rect, circle, ellipse, line, polyline, polygon, path, text, tspan, image, linearGradient, radialGradient, stop, marker, filter (feDropShadow only), title, desc"

// convertSVG validates src and converts it to scene nodes.
func (d *deck) convertSVG(src string) (*svgScene, error) {
	root, err := parseSVG(src)
	if err != nil {
		return nil, err
	}
	c := &svgConv{d: d, ids: map[string]*svgNode{}, grads: map[string]*svgGrad{}, warnSeen: map[string]bool{}}
	c.index(root)

	vb, err := numberList(root.get("viewBox"))
	if err != nil || len(vb) != 4 {
		w, e1 := svgLength(root.get("width"))
		h, e2 := svgLength(root.get("height"))
		if e1 != nil || e2 != nil {
			return nil, errors.New(`the root <svg> needs viewBox="0 0 1280 720"`)
		}
		vb = []float64{0, 0, w, h}
		c.warnf(root, `no viewBox: using width/height as the canvas; write viewBox="0 0 1280 720"`)
	}
	if vb[2] <= 0 || vb[3] <= 0 {
		return nil, errors.New("the viewBox must have a positive width and height")
	}
	if r := vb[2] / vb[3]; math.Abs(r-16.0/9) > 0.02 {
		return nil, fmt.Errorf("the viewBox %gx%g is not 16:9; use viewBox=\"0 0 1280 720\"", vb[2], vb[3])
	}
	c.k = slideW / vb[2]
	m0 := affine{a: c.k, d: c.k, e: -vb[0] * c.k, f: -vb[1] * c.k}

	var nodes []*scNode
	rst := c.style(root, rootStyle())
	for _, kid := range root.kids {
		c.walk(kid, rst, m0, &nodes)
	}
	if c.nerr > 0 {
		return nil, c.problem()
	}
	if len(nodes) == 0 {
		return nil, errors.New("the SVG draws nothing")
	}
	sc := &svgScene{nodes: nodes, warnings: c.warns}
	if bg, ok := backgroundOf(nodes[0]); ok {
		sc.bg = bg
		sc.nodes = nodes[1:]
	}
	return sc, nil
}

func (c *svgConv) problem() error {
	var b strings.Builder
	fmt.Fprintf(&b, "the SVG has %d problem(s); fix them and resend the whole slide:", c.nerr)
	for i, e := range c.errs {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, e)
	}
	if c.nerr > len(c.errs) {
		fmt.Fprintf(&b, "\n  ... and %d more", c.nerr-len(c.errs))
	}
	return errors.New(b.String())
}

// index records ids and enforces the element whitelist.
func (c *svgConv) index(n *svgNode) {
	if n.tag == "#text" {
		return
	}
	if id := n.attr["id"]; id != "" {
		if _, dup := c.ids[id]; dup {
			c.warnf(n, "id %q is used twice", id)
		}
		c.ids[id] = n
	}
	if !svgDrawing[n.tag] && !svgDefsOnly[n.tag] {
		if why, ok := svgRefusals[n.tag]; ok {
			c.errf(n, "<%s> is not supported: %s", n.tag, why)
		} else {
			c.errf(n, "<%s> is not supported; the allowed elements are %s", n.tag, svgAllowed)
		}
		return
	}
	for _, name := range []string{"clip-path", "mask"} {
		if v := n.get(name); v != "" && v != "none" {
			c.errf(n, "%s is not supported; draw the shape at its final size", name)
		}
	}
	for _, k := range n.kids {
		c.index(k)
	}
}

func (c *svgConv) num(n *svgNode, name string, def float64) float64 {
	v, ok := n.attr[name]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	f, err := svgLength(v)
	if err != nil {
		c.errf(n, "%s: %v", name, err)
		return def
	}
	return f
}

// frac reads an opacity: 0..1 or a percentage.
func (c *svgConv) frac(n *svgNode, name, v string) float64 {
	s := strings.TrimSpace(v)
	scale := 1.0
	if strings.HasSuffix(s, "%") {
		s, scale = strings.TrimSuffix(s, "%"), 0.01
	}
	f, err := svgLength(s)
	if err != nil {
		c.errf(n, "%s: %v", name, err)
		return 1
	}
	return math.Max(0, math.Min(1, f*scale))
}

// style resolves n's presentation attributes on top of the inherited style.
func (c *svgConv) style(n *svgNode, p svgStyle) svgStyle {
	s := p
	for name, v := range n.attr {
		switch name {
		case "opacity":
			s.opacity = p.opacity * c.frac(n, name, v)
		case "fill-opacity":
			s.fillOp = c.frac(n, name, v)
		case "stroke-opacity":
			s.strokeOp = c.frac(n, name, v)
		case "fill":
			s.fill = c.paint(n, name, v)
		case "stroke":
			s.stroke = c.paint(n, name, v)
		case "stroke-width":
			s.strokeW = math.Max(0, c.num(n, name, 1))
		case "stroke-dasharray":
			s.dash = nil
			if v = strings.TrimSpace(v); v != "" && v != "none" {
				if l, err := numberList(v); err != nil {
					c.errf(n, "stroke-dasharray: %v", err)
				} else {
					s.dash = l
				}
			}
		case "stroke-linecap":
			s.cap = strings.TrimSpace(v)
		case "stroke-linejoin":
			s.join = strings.TrimSpace(v)
		case "font-family":
			s.fontFamily = v
		case "font-size":
			s.fontSize = c.num(n, name, p.fontSize)
			if s.fontSize <= 0 {
				c.errf(n, "font-size must be positive")
				s.fontSize = p.fontSize
			}
		case "font-weight":
			w := strings.ToLower(strings.TrimSpace(v))
			s.bold = w == "bold" || w == "bolder" || w >= "600" && len(w) == 3
		case "font-style":
			s.italic = strings.Contains(v, "italic") || strings.Contains(v, "oblique")
		case "text-decoration", "text-decoration-line":
			s.under = strings.Contains(v, "underline")
			s.strike = strings.Contains(v, "line-through")
		case "text-anchor":
			s.anchor = strings.TrimSpace(v)
		case "dominant-baseline", "alignment-baseline":
			s.baseline = strings.TrimSpace(v)
		case "letter-spacing":
			if v = strings.TrimSpace(v); v != "normal" {
				s.spacing = c.num(n, name, 0)
			}
		case "filter":
			s.filter = refID(v)
		case "marker-start":
			s.markerStart = refID(v) != ""
		case "marker-end":
			s.markerEnd = refID(v) != ""
		case "marker":
			s.markerStart, s.markerEnd = refID(v) != "", refID(v) != ""
		}
	}
	return s
}

// refID extracts the id from url(#id); "" for none or anything else.
func refID(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "url(") {
		return ""
	}
	inner := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(v, "url("), ")"), `"' `)
	return strings.TrimPrefix(inner, "#")
}

func (c *svgConv) paint(n *svgNode, prop, v string) svgPaint {
	if strings.HasPrefix(strings.TrimSpace(v), "url(") {
		id := refID(v)
		g := c.gradient(n, id)
		if g == nil {
			return noPaint
		}
		if len(g.stops) == 1 {
			return svgPaint{rgb: g.stops[0].rgb, alpha: g.stops[0].alpha}
		}
		return svgPaint{grad: g, alpha: 1}
	}
	rgb, a, none, err := c.color(v)
	if err != nil {
		c.errf(n, "%s: %v", prop, err)
		return noPaint
	}
	if none {
		return noPaint
	}
	return svgPaint{rgb: rgb, alpha: a}
}

// color reads a colour: a theme token (primary, secondary, accent, surface,
// text, muted, line, soft, chart1..chart6) or anything parseColor accepts.
// Tokens keep an SVG slide in the same palette as the deck's other slides.
func (c *svgConv) color(s string) (rgb string, alpha float64, none bool, err error) {
	p := c.d.pal
	tok := map[string]string{
		"primary": p.Primary, "secondary": p.Secondary, "accent": p.Accent, "surface": p.Surface,
		"text": p.Text, "muted": p.Muted, "line": p.Line, "soft": p.Soft,
	}
	for i, ch := range p.Chart {
		tok[fmt.Sprintf("chart%d", i+1)] = ch
	}
	if h, ok := tok[strings.ToLower(strings.TrimSpace(s))]; ok {
		return h, 1, false, nil
	}
	rgb, alpha, none, err = parseColor(s)
	if err != nil {
		err = fmt.Errorf("%w; theme colours are primary, secondary, accent, surface, text, muted, line, soft, chart1..chart%d", err, len(p.Chart))
	}
	return rgb, alpha, none, err
}

// pct reads a gradient coordinate: "50%" -> 0.5, a plain number as is.
func pct(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	scale := 1.0
	if strings.HasSuffix(s, "%") {
		s, scale = strings.TrimSuffix(s, "%"), 0.01
	}
	f, err := svgLength(s)
	if err != nil {
		return def
	}
	return f * scale
}

func (c *svgConv) gradient(use *svgNode, id string) *svgGrad {
	if g, ok := c.grads[id]; ok {
		return g
	}
	n := c.ids[id]
	if n == nil || (n.tag != "linearGradient" && n.tag != "radialGradient") {
		c.errf(use, "fill/stroke refers to #%s, which is not a <linearGradient> or <radialGradient> in this SVG", id)
		return nil
	}
	g := &svgGrad{radial: n.tag == "radialGradient", userSpace: n.get("gradientUnits") == "userSpaceOnUse"}
	g.x1, g.y1, g.x2, g.y2 = pct(n.attr["x1"], 0), pct(n.attr["y1"], 0), pct(n.attr["x2"], 1), pct(n.attr["y2"], 0)
	g.cx, g.cy = pct(n.attr["cx"], 0.5), pct(n.attr["cy"], 0.5)
	src := n
	if len(childrenByTag(src, "stop")) == 0 { // stops may come from a referenced gradient
		if ref := c.ids[strings.TrimPrefix(n.get("href"), "#")]; ref != nil {
			src = ref
		}
	}
	last := 0.0
	for _, st := range childrenByTag(src, "stop") {
		off := math.Max(last, math.Min(1, math.Max(0, pct(st.attr["offset"], 0))))
		last = off
		rgb, a, none, err := c.color(firstNonEmpty(st.get("stop-color"), "#000000"))
		if err != nil {
			c.errf(st, "stop-color: %v", err)
			continue
		}
		if none {
			rgb, a = "000000", 0
		}
		if v, ok := st.attr["stop-opacity"]; ok {
			a *= c.frac(st, "stop-opacity", v)
		}
		g.stops = append(g.stops, svgStop{pos: off, rgb: rgb, alpha: a})
	}
	if len(g.stops) == 0 || len(g.stops) > 10 {
		c.errf(n, "gradient #%s has %d <stop> elements; PowerPoint takes 2 to 10", id, len(g.stops))
		c.grads[id] = nil
		return nil
	}
	c.grads[id] = g
	return g
}

func childrenByTag(n *svgNode, tag string) []*svgNode {
	var out []*svgNode
	for _, k := range n.kids {
		if k.tag == tag {
			out = append(out, k)
		}
	}
	return out
}

// withOpacity folds an element opacity into a paint.
func withOpacity(p svgPaint, op float64) svgPaint {
	if p.none || op >= 0.999 {
		return p
	}
	p.alpha *= op
	if p.grad != nil {
		g := *p.grad
		g.stops = append([]svgStop(nil), g.stops...)
		for i := range g.stops {
			g.stops[i].alpha *= op
		}
		p.grad = &g
	}
	return p
}

func dashPreset(dash []float64, w float64) string {
	if len(dash) == 0 || w <= 0 || dash[0] <= 0 {
		return ""
	}
	if len(dash) >= 4 {
		return "dashDot"
	}
	switch a := dash[0] / w; {
	case a <= 1.5:
		return "sysDot"
	case a <= 3.5:
		return "sysDash"
	case a <= 5.5:
		return "dash"
	}
	return "lgDash"
}

// look copies the paint, stroke and effects of st onto nd.
func (c *svgConv) look(nd *scNode, n *svgNode, st svgStyle, m affine, shadow bool) {
	sf := m.scaleFactor()
	nd.fill = withOpacity(st.fill, st.fillOp*st.opacity)
	nd.stroke = withOpacity(st.stroke, st.strokeOp*st.opacity)
	if nd.stroke.grad != nil { // a gradient line is not worth the complexity: use its first colour
		c.warnf(n, "gradient strokes are drawn in the first stop colour")
		g := nd.stroke.grad.stops[0]
		nd.stroke = svgPaint{rgb: g.rgb, alpha: g.alpha}
	}
	nd.strokeW = st.strokeW * sf * ptPerInch
	if !nd.stroke.none && nd.strokeW < 0.25 {
		nd.strokeW = 0.25
	}
	nd.dash = dashPreset(st.dash, st.strokeW)
	nd.cap, nd.join = st.cap, st.join
	nd.arrowStart, nd.arrowEnd = st.markerStart, st.markerEnd
	if shadow {
		nd.shadow = c.shadow(n, st, sf)
	}
	if t := firstChild(n, "title"); t != nil {
		nd.descr = strings.TrimSpace(textOf(t))
	}
	if id := n.get("id"); id != "" {
		nd.name = id
	}
}

func firstChild(n *svgNode, tag string) *svgNode {
	for _, k := range n.kids {
		if k.tag == tag {
			return k
		}
	}
	return nil
}

func textOf(n *svgNode) string {
	var b strings.Builder
	for _, k := range n.kids {
		if k.tag == "#text" {
			b.WriteString(k.text)
		}
	}
	return b.String()
}

func (c *svgConv) shadow(n *svgNode, st svgStyle, sf float64) *scShadow {
	if st.filter == "" {
		return nil
	}
	f := c.ids[st.filter]
	if f == nil || f.tag != "filter" {
		c.warnf(n, "filter #%s is not defined in this SVG; ignored", st.filter)
		return nil
	}
	fe := firstChild(f, "feDropShadow")
	if fe == nil {
		c.warnf(f, "this filter has no <feDropShadow>, so it is ignored")
		return nil
	}
	dx, dy, sd := c.num(fe, "dx", 2), c.num(fe, "dy", 2), c.num(fe, "stdDeviation", 2)
	rgb, a, none, err := c.color(firstNonEmpty(fe.get("flood-color"), "#000000"))
	if err != nil || none {
		rgb, a = "000000", 1
	}
	if v, ok := fe.attr["flood-opacity"]; ok {
		a *= c.frac(fe, "flood-opacity", v)
	} else {
		a *= 0.35 // SVG's default flood is opaque, which looks heavy as a slide shadow
	}
	dir := math.Atan2(dy, dx) * 180 / math.Pi
	if dir < 0 {
		dir += 360
	}
	return &scShadow{blurPt: sd * 2 * sf * ptPerInch, distPt: math.Hypot(dx, dy) * sf * ptPerInch, dirDeg: dir, rgb: rgb, alpha: a * st.opacity}
}

func (c *svgConv) count(n *svgNode) bool {
	c.shapes++
	if c.shapes == maxSVGShapes+1 {
		c.errf(n, "the slide has more than %d shapes; simplify it", maxSVGShapes)
	}
	return c.shapes <= maxSVGShapes
}

func (c *svgConv) walk(n *svgNode, parent svgStyle, pm affine, out *[]*scNode) {
	switch n.tag {
	case "#text", "title", "desc", "metadata", "defs", "linearGradient", "radialGradient", "stop", "marker", "filter", "feDropShadow":
		return
	}
	if n.get("display") == "none" || n.get("visibility") == "hidden" {
		return
	}
	st := c.style(n, parent)
	m := pm
	if t := n.get("transform"); t != "" {
		tm, err := parseTransform(t)
		if err != nil {
			c.errf(n, "%v", err)
			return
		}
		m = pm.mul(tm)
	}
	switch n.tag {
	case "g":
		var kids []*scNode
		for _, k := range n.kids {
			c.walk(k, st, m, &kids)
		}
		switch {
		case len(kids) == 0:
		case len(kids) == 1 && n.get("id") == "":
			*out = append(*out, kids[0])
		default:
			g := &scNode{kind: "group", name: n.get("id"), kids: kids}
			g.x, g.y, g.w, g.h = unionBox(kids)
			*out = append(*out, g)
		}
	case "rect":
		c.rect(n, st, m, out)
	case "circle", "ellipse":
		c.ellipse(n, st, m, out)
	case "line":
		c.line(n, st, m, out)
	case "polyline", "polygon":
		c.poly(n, st, m, out)
	case "path":
		c.path(n, st, m, out)
	case "text":
		c.text(n, st, m, out)
	case "image":
		c.image(n, st, m, out)
	case "tspan":
		c.errf(n, "<tspan> must be inside <text>")
	case "svg":
		c.errf(n, "a nested <svg> is not supported; use <g transform=\"translate(x y)\"> instead")
	}
}

func unionBox(kids []*scNode) (x, y, w, h float64) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, k := range kids {
		x0, y0 = math.Min(x0, k.x), math.Min(y0, k.y)
		x1, y1 = math.Max(x1, k.x+k.w), math.Max(y1, k.y+k.h)
	}
	return x0, y0, x1 - x0, y1 - y0
}

// frame maps the user-space box through m. ok is false when m skews or mirrors
// a rotated box, which a PowerPoint frame cannot express.
func frame(m affine, x, y, w, h float64) (fx, fy, fw, fh, rot float64, ok bool) {
	if m.axisAligned() {
		p0, p1 := m.apply(pt{x, y}), m.apply(pt{x + w, y + h})
		return math.Min(p0.x, p1.x), math.Min(p0.y, p1.y), math.Abs(p1.x - p0.x), math.Abs(p1.y - p0.y), 0, true
	}
	if s, deg, ok := m.similarity(); ok {
		cc := m.apply(pt{x + w/2, y + h/2})
		fw, fh = w*s, h*s
		return cc.x - fw/2, cc.y - fh/2, fw, fh, deg, true
	}
	return 0, 0, 0, 0, 0, false
}

func (c *svgConv) pathNode(n *svgNode, st svgStyle, m affine, segs []pathSeg, out *[]*scNode) {
	if !c.count(n) {
		return
	}
	segs = transformSegs(segs, m)
	x0, y0, x1, y1, ok := segBounds(segs)
	if !ok {
		return
	}
	nd := &scNode{kind: "path", segs: segs, x: x0, y: y0, w: x1 - x0, h: y1 - y0}
	c.look(nd, n, st, m, true)
	*out = append(*out, nd)
}

func (c *svgConv) rect(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	if !n.has("width") || !n.has("height") {
		c.errf(n, "<rect> needs width and height")
		return
	}
	x, y, w, h := c.num(n, "x", 0), c.num(n, "y", 0), c.num(n, "width", 0), c.num(n, "height", 0)
	if w <= 0 || h <= 0 {
		return
	}
	rx, ry := c.num(n, "rx", -1), c.num(n, "ry", -1)
	if rx < 0 {
		rx = ry
	}
	if ry < 0 {
		ry = rx
	}
	rx = math.Max(0, math.Min(rx, w/2))
	if fx, fy, fw, fh, rot, ok := frame(m, x, y, w, h); ok {
		if !c.count(n) {
			return
		}
		nd := &scNode{kind: "rect", x: fx, y: fy, w: fw, h: fh, rot: rot}
		if rx > 0 {
			nd.radius = rx * math.Min(fw/w, fh/h)
		}
		c.look(nd, n, st, m, true)
		*out = append(*out, nd)
		return
	}
	c.warnf(n, "a rectangle that is skewed or mirrored is drawn as a plain polygon (rounded corners lost)")
	c.pathNode(n, st, m, []pathSeg{
		{op: 'M', p: [3]pt{{x, y}}}, {op: 'L', p: [3]pt{{x + w, y}}},
		{op: 'L', p: [3]pt{{x + w, y + h}}}, {op: 'L', p: [3]pt{{x, y + h}}}, {op: 'Z'},
	}, out)
}

func (c *svgConv) ellipse(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	cx, cy := c.num(n, "cx", 0), c.num(n, "cy", 0)
	rx, ry := c.num(n, "rx", 0), c.num(n, "ry", 0)
	if n.tag == "circle" {
		if !n.has("r") {
			c.errf(n, "<circle> needs r")
			return
		}
		rx = c.num(n, "r", 0)
		ry = rx
	} else if !n.has("rx") || !n.has("ry") {
		c.errf(n, "<ellipse> needs rx and ry")
		return
	}
	if rx <= 0 || ry <= 0 {
		return
	}
	if fx, fy, fw, fh, rot, ok := frame(m, cx-rx, cy-ry, 2*rx, 2*ry); ok {
		if !c.count(n) {
			return
		}
		nd := &scNode{kind: "ellipse", x: fx, y: fy, w: fw, h: fh, rot: rot}
		c.look(nd, n, st, m, true)
		*out = append(*out, nd)
		return
	}
	c.pathNode(n, st, m, ellipseSegs(cx, cy, rx, ry), out)
}

func (c *svgConv) line(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	if st.stroke.none {
		c.warnf(n, "a <line> without a stroke colour is invisible; add stroke=\"#...\"")
		return
	}
	if !c.count(n) {
		return
	}
	p1 := m.apply(pt{c.num(n, "x1", 0), c.num(n, "y1", 0)})
	p2 := m.apply(pt{c.num(n, "x2", 0), c.num(n, "y2", 0)})
	nd := &scNode{
		kind: "line", x: math.Min(p1.x, p2.x), y: math.Min(p1.y, p2.y),
		w: math.Abs(p2.x - p1.x), h: math.Abs(p2.y - p1.y), flipH: p2.x < p1.x, flipV: p2.y < p1.y,
	}
	c.look(nd, n, st, m, true)
	nd.fill = noPaint
	*out = append(*out, nd)
}

func (c *svgConv) poly(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	vals, err := numberList(n.get("points"))
	if err != nil || len(vals) < 4 || len(vals)%2 != 0 {
		c.errf(n, "points must be x,y pairs (at least two points)")
		return
	}
	var segs []pathSeg
	for i := 0; i < len(vals); i += 2 {
		op := byte('L')
		if i == 0 {
			op = 'M'
		}
		segs = append(segs, pathSeg{op: op, p: [3]pt{{vals[i], vals[i+1]}}})
	}
	if n.tag == "polygon" {
		segs = append(segs, pathSeg{op: 'Z'})
	}
	c.pathNode(n, st, m, segs, out)
}

func (c *svgConv) path(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	segs, err := parsePathData(n.get("d"))
	if err != nil {
		c.errf(n, "d: %v", err)
		return
	}
	c.pathNode(n, st, m, segs, out)
}

// ---- images ---------------------------------------------------------------

func (c *svgConv) image(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	href := strings.TrimSpace(n.attr["href"])
	if href == "" {
		c.errf(n, "<image> needs href (a workspace-relative .png/.jpg path or a data: URI)")
		return
	}
	var data []byte
	var ext string
	if strings.HasPrefix(href, "data:") {
		head, body, ok := strings.Cut(href, ",")
		if !ok || !strings.HasSuffix(head, ";base64") {
			c.errf(n, "data: URIs must be base64")
			return
		}
		switch {
		case strings.HasPrefix(head, "data:image/png"):
			ext = ".png"
		case strings.HasPrefix(head, "data:image/jpeg"), strings.HasPrefix(head, "data:image/jpg"):
			ext = ".jpg"
		default:
			c.errf(n, "only png and jpeg images are supported")
			return
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(body)); err != nil {
			c.errf(n, "the base64 image data is not valid")
			return
		}
	} else {
		if strings.Contains(href, "://") {
			c.errf(n, "remote images are not supported; use a workspace-relative path")
			return
		}
		ext = strings.ToLower(filepath.Ext(href))
		if ext == ".jpeg" {
			ext = ".jpg"
		}
		if ext != ".png" && ext != ".jpg" {
			c.errf(n, "images must be .png or .jpg")
			return
		}
		abs, err := c.d.env.Resolve(href)
		if err != nil {
			c.errf(n, "href: %v", err)
			return
		}
		if data, err = os.ReadFile(abs); err != nil {
			c.errf(n, "href %s: %v", href, err)
			return
		}
	}
	if len(data) > maxImageBytes {
		c.errf(n, "the image is too large (%d bytes, max %d)", len(data), maxImageBytes)
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		c.errf(n, "the image is not a readable picture")
		return
	}
	if !n.has("width") || !n.has("height") {
		c.errf(n, "<image> needs width and height")
		return
	}
	x, y, w, h := c.num(n, "x", 0), c.num(n, "y", 0), c.num(n, "width", 0), c.num(n, "height", 0)
	if w <= 0 || h <= 0 {
		return
	}
	if par := strings.Fields(n.get("preserveAspectRatio")); len(par) == 0 || par[0] != "none" { // meet: fit inside, centred
		s := math.Min(w/float64(cfg.Width), h/float64(cfg.Height))
		iw, ih := float64(cfg.Width)*s, float64(cfg.Height)*s
		x, y, w, h = x+(w-iw)/2, y+(h-ih)/2, iw, ih
	}
	fx, fy, fw, fh, rot, ok := frame(m, x, y, w, h)
	if !ok {
		c.errf(n, "an <image> can only be moved, scaled uniformly or rotated")
		return
	}
	if !c.count(n) {
		return
	}
	nd := &scNode{kind: "image", x: fx, y: fy, w: fw, h: fh, rot: rot, img: data, imgExt: ext, descr: strings.TrimSpace(n.attr["aria-label"])}
	if t := firstChild(n, "title"); t != nil {
		nd.descr = strings.TrimSpace(textOf(t))
	}
	nd.name = n.get("id")
	*out = append(*out, nd)
}

// ---- text -----------------------------------------------------------------

type textBuilder struct {
	lines        []*scLine
	cur          *scLine
	lastX, lastY float64
}

func (tb *textBuilder) newLine(x, y float64) {
	if tb.cur != nil && len(tb.cur.runs) > 0 {
		tb.lines = append(tb.lines, tb.cur)
	}
	tb.cur = &scLine{x: x, y: y}
	tb.lastX, tb.lastY = x, y
}

// collapse folds whitespace runs to one space, as SVG does.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

func (c *svgConv) runOf(text string, st svgStyle) scRun {
	r := scRun{
		text: text, size: st.fontSize, bold: st.bold, italic: st.italic, under: st.under, strike: st.strike,
		font: pickFont(st.fontFamily), spacing: st.spacing,
	}
	p := withOpacity(st.fill, st.fillOp*st.opacity)
	switch {
	case p.none:
		r.rgb, r.alpha = "", 0
	case p.grad != nil:
		r.rgb, r.alpha = p.grad.stops[0].rgb, p.grad.stops[0].alpha
	default:
		r.rgb, r.alpha = p.rgb, p.alpha
	}
	return r
}

func (tb *textBuilder) add(text string, r scRun) {
	t := collapse(text)
	if t == "" {
		return
	}
	if len(tb.cur.runs) == 0 {
		t = strings.TrimLeft(t, " ")
	} else if last := tb.cur.runs[len(tb.cur.runs)-1]; strings.HasSuffix(last.text, " ") {
		t = strings.TrimLeft(t, " ")
	}
	if t == "" {
		return
	}
	r.text = t
	if n := len(tb.cur.runs); n > 0 {
		prev := &tb.cur.runs[n-1]
		same := *prev
		same.text = ""
		cmp := r
		cmp.text = ""
		if same == cmp {
			prev.text += t
			return
		}
	}
	tb.cur.runs = append(tb.cur.runs, r)
}

func (c *svgConv) collect(tb *textBuilder, n *svgNode, st svgStyle) {
	for _, k := range n.kids {
		switch k.tag {
		case "#text":
			if strings.Contains(strings.TrimSpace(k.text), "\n") {
				c.warnf(n, "a line break inside <text> is just a space in SVG; write one <tspan x=... y=...> per line")
			}
			tb.add(k.text, c.runOf(k.text, st))
		case "tspan":
			ks := c.style(k, st)
			newLine := k.has("x") || k.has("y")
			if !newLine && k.has("dy") && math.Abs(c.num(k, "dy", 0)) >= 0.6*ks.fontSize {
				newLine = true
			}
			if newLine {
				x, y := tb.lastX, tb.lastY
				if k.has("x") {
					x = c.num(k, "x", x)
				}
				if k.has("y") {
					y = c.num(k, "y", y)
				}
				tb.newLine(x, y+c.num(k, "dy", 0))
			}
			c.collect(tb, k, ks)
		case "title", "desc":
		default:
			c.errf(k, "<%s> inside <text> is not supported; use <tspan>", k.tag)
		}
	}
}

func lineText(l *scLine) string {
	var b strings.Builder
	for _, r := range l.runs {
		b.WriteString(r.text)
	}
	return b.String()
}

func lineWidth(l *scLine) float64 {
	var w float64
	for _, r := range l.runs {
		f := 1.0
		if r.bold {
			f = 1.04
		}
		for _, ch := range r.text {
			w += charEm(ch)*r.size*f + r.spacing
		}
	}
	return w
}

func (c *svgConv) text(n *svgNode, st svgStyle, m affine, out *[]*scNode) {
	if !m.axisAligned() {
		if _, _, ok := m.similarity(); !ok {
			c.errf(n, "text can only be moved, scaled uniformly or rotated (no skew or mirroring)")
			return
		}
	}
	for _, name := range []string{"x", "y", "dx", "dy"} {
		if strings.ContainsAny(strings.TrimSpace(n.attr[name]), " ,") {
			c.errf(n, "%s lists several values (per-character positions); put one value on <text> and use one <tspan> per line", name)
			return
		}
	}
	tb := &textBuilder{}
	tb.newLine(c.num(n, "x", 0), c.num(n, "y", 0)+c.num(n, "dy", 0))
	c.collect(tb, n, st)
	tb.newLine(0, 0) // flush the last line
	if len(tb.lines) == 0 {
		return
	}
	sf := m.scaleFactor()
	for _, l := range tb.lines {
		last := &l.runs[len(l.runs)-1]
		last.text = strings.TrimRight(last.text, " ")
		if last.text == "" && len(l.runs) > 1 {
			l.runs = l.runs[:len(l.runs)-1]
		}
	}
	// One text box per run of lines that share a left edge.
	for i := 0; i < len(tb.lines); {
		j := i + 1
		for j < len(tb.lines) && math.Abs(tb.lines[j].x-tb.lines[i].x) < 0.5 {
			j++
		}
		c.textBox(n, st, m, sf, tb.lines[i:j], out)
		i = j
	}
}

// textBox lays out lines that share x as one PowerPoint text box.
func (c *svgConv) textBox(n *svgNode, st svgStyle, m affine, sf float64, lines []*scLine, out *[]*scNode) {
	if !c.count(n) {
		return
	}
	lineSize := func(l *scLine) float64 {
		s := 0.0
		for _, r := range l.runs {
			s = math.Max(s, r.size)
		}
		return s
	}
	shift := func(size float64) float64 { // dominant-baseline: move the baseline to where y really is
		switch st.baseline {
		case "middle", "central":
			return 0.35 * size
		case "hanging", "text-before-edge":
			return 0.8 * size
		case "text-after-edge":
			return -0.2 * size
		}
		return 0
	}
	first := lines[0]
	size0 := lineSize(first)
	// Line i has exact pitch P_i and its baseline sits at top_i + K*P_i - S*size_i,
	// where top_i is the bottom of line i-1. The first line borrows the pitch of
	// the second so a block of equal lines gets one uniform pitch.
	fit := func(pitch, size float64) float64 { return svgFitK*pitch - svgFitS*size }
	prevBase, prevSize := first.y+shift(size0), size0
	first.pitch = svgNatural * size0
	if len(lines) > 1 {
		l := lines[1]
		first.pitch = math.Max(0.5*size0, l.y+shift(lineSize(l))-prevBase)
	}
	top := prevBase - fit(first.pitch, size0)
	height := first.pitch
	prevPitch := first.pitch
	width := lineWidth(first)
	for _, l := range lines[1:] {
		size := lineSize(l)
		base := l.y + shift(size)
		// base = top_i + fit(P_i), top_i = previous top + previous pitch, and the
		// previous baseline = previous top + fit(previous pitch).
		p := (base - prevBase + fit(prevPitch, prevSize) - prevPitch + svgFitS*size) / svgFitK
		l.pitch = math.Max(0.5*size, p)
		height += l.pitch
		width = math.Max(width, lineWidth(l))
		prevBase, prevSize, prevPitch = base, size, l.pitch
	}
	for _, l := range lines {
		l.pitch *= sf * ptPerInch
	}
	width = width*1.04 + 0.5
	x := first.x
	switch st.anchor {
	case "middle":
		x -= width / 2
	case "end":
		x -= width
	}
	fx, fy, fw, fh, rot, ok := frame(m, x, top, width, height)
	if !ok {
		c.errf(n, "text can only be moved, scaled uniformly or rotated")
		return
	}
	nd := &scNode{kind: "text", x: fx, y: fy, w: fw, h: fh, rot: rot, anchor: st.anchor}
	if rot == 0 && (fx < -0.05 || fx+fw > slideW+0.05) {
		c.warnf(n, "the text %q is about %.0f px wide and runs off the slide; split it into lines", truncateRunes(lineText(first), 20), width*sf/c.k)
	}
	if n.get("id") == "title" && !c.title {
		nd.title, c.title = true, true
	}
	nd.name = n.get("id")
	for _, l := range lines {
		cl := scLine{pitch: l.pitch}
		for _, r := range l.runs {
			r.size *= sf * ptPerInch
			r.spacing *= sf * ptPerInch
			cl.runs = append(cl.runs, r)
		}
		nd.lines = append(nd.lines, cl)
	}
	if t := firstChild(n, "title"); t != nil {
		nd.descr = strings.TrimSpace(textOf(t))
	}
	*out = append(*out, nd)
}

// ---- fonts ----------------------------------------------------------------

var svgKnownFonts = map[string]string{
	"microsoft yahei": "Microsoft YaHei", "微软雅黑": "Microsoft YaHei", "simsun": "SimSun", "宋体": "SimSun",
	"simhei": "SimHei", "黑体": "SimHei", "kaiti": "KaiTi", "楷体": "KaiTi", "fangsong": "FangSong", "仿宋": "FangSong",
	"arial": "Arial", "times new roman": "Times New Roman", "calibri": "Calibri", "consolas": "Consolas",
	"segoe ui": "Segoe UI", "courier new": "Courier New", "georgia": "Georgia", "verdana": "Verdana",
	"tahoma": "Tahoma", "monospace": "Consolas", "serif": "SimSun",
}

// pickFont returns the first listed family that is a known Windows font, or ""
// for the deck font (sans-serif, unknown names).
func pickFont(family string) string {
	for _, f := range strings.Split(family, ",") {
		f = strings.ToLower(strings.Trim(strings.TrimSpace(f), `"'`))
		if name, ok := svgKnownFonts[f]; ok {
			return name
		}
	}
	return ""
}

// ---- background -----------------------------------------------------------

// backgroundOf recognises a full-slide rectangle drawn first and turns it into
// the slide background, which is how a designer would build it.
func backgroundOf(nd *scNode) (string, bool) {
	if nd.kind != "rect" || nd.rot != 0 || nd.radius != 0 || nd.shadow != nil || !nd.stroke.none || nd.fill.none {
		return "", false
	}
	if nd.x > 0.02 || nd.y > 0.02 || nd.x+nd.w < slideW-0.02 || nd.y+nd.h < slideH-0.02 {
		return "", false
	}
	return `<p:bg><p:bgPr>` + svgFillXML(nd.fill, nd.w, nd.h) + `<a:effectLst/></p:bgPr></p:bg>`, true
}
