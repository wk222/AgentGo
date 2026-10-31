package officedoc

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// The SVG side of the pipeline: a strict XML parse into a small tree, the
// element whitelist, colours and style resolution. Anything outside the
// whitelist is reported (with its line) so the model can fix its SVG; nothing
// is silently dropped when it would change how the slide looks.

const (
	maxSVGBytes = 400 << 10
	maxSVGNodes = 4000
	maxSVGDepth = 20
)

type svgNode struct {
	tag    string // local element name, or "#text"
	attr   map[string]string
	kids   []*svgNode
	text   string // "#text" only
	line   int
	parent *svgNode
}

func (n *svgNode) get(name string) string { return strings.TrimSpace(n.attr[name]) }

func (n *svgNode) has(name string) bool { _, ok := n.attr[name]; return ok }

// label names the element for messages.
func (n *svgNode) label() string {
	s := "<" + n.tag
	if id := n.attr["id"]; id != "" {
		s += ` id="` + truncateRunes(id, 24) + `"`
	}
	return fmt.Sprintf("line %d %s>", n.line, s)
}

const svgNS = "http://www.w3.org/2000/svg"

// parseSVG parses src into a tree. It rejects DOCTYPE/entity declarations and
// anything that is not well-formed XML.
func parseSVG(src string) (*svgNode, error) {
	if len(src) > maxSVGBytes {
		return nil, fmt.Errorf("svg is %d bytes; the limit is %d (split the slide or simplify the drawing)", len(src), maxSVGBytes)
	}
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	var root, cur *svgNode
	nodes, depth := 0, 0
	lineAt := func() int { return strings.Count(src[:min(int(dec.InputOffset()), len(src))], "\n") + 1 }
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the SVG is not well-formed XML (line %d): %v", lineAt(), err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("line %d: DOCTYPE and ENTITY declarations are not allowed", lineAt())
		case xml.StartElement:
			nodes++
			depth++
			if nodes > maxSVGNodes {
				return nil, fmt.Errorf("the SVG has more than %d elements; simplify it", maxSVGNodes)
			}
			if depth > maxSVGDepth {
				return nil, fmt.Errorf("line %d: elements are nested more than %d levels deep", lineAt(), maxSVGDepth)
			}
			n := &svgNode{tag: t.Name.Local, attr: map[string]string{}, line: lineAt(), parent: cur}
			if t.Name.Space != "" && t.Name.Space != svgNS {
				n.tag = t.Name.Space + ":" + t.Name.Local
			}
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns" || a.Name.Local == "xmlns":
					continue
				case a.Name.Space == "" || strings.Contains(a.Name.Space, "xlink"):
					n.attr[a.Name.Local] = a.Value
				}
			}
			if st := n.attr["style"]; st != "" {
				for _, decl := range strings.Split(st, ";") {
					k, v, ok := strings.Cut(decl, ":")
					if k = strings.TrimSpace(k); ok && k != "" {
						n.attr[k] = strings.TrimSpace(v) // a style declaration beats a presentation attribute
					}
				}
				delete(n.attr, "style")
			}
			if cur == nil {
				if root != nil {
					return nil, errors.New("the SVG has more than one root element")
				}
				root = n
			} else {
				cur.kids = append(cur.kids, n)
			}
			cur = n
		case xml.EndElement:
			depth--
			if cur != nil {
				cur = cur.parent
			}
		case xml.CharData:
			if cur != nil && len(t) > 0 {
				cur.kids = append(cur.kids, &svgNode{tag: "#text", text: string(t), line: lineAt(), parent: cur})
			}
		}
	}
	if root == nil {
		return nil, errors.New("the svg field is empty")
	}
	if root.tag != "svg" {
		return nil, fmt.Errorf("the root element must be <svg>, found <%s>", root.tag)
	}
	return root, nil
}

// Element whitelist. Drawing elements first, then the ones that only make
// sense inside <defs>.
var svgDrawing = map[string]bool{
	"svg": true, "g": true, "rect": true, "circle": true, "ellipse": true, "line": true,
	"polyline": true, "polygon": true, "path": true, "text": true, "tspan": true, "image": true,
	"title": true, "desc": true, "metadata": true, "defs": true,
}

var svgDefsOnly = map[string]bool{
	"linearGradient": true, "radialGradient": true, "stop": true, "marker": true,
	"filter": true, "feDropShadow": true,
}

// svgRefusals explains the elements models reach for most that cannot become
// native PowerPoint objects.
var svgRefusals = map[string]string{
	"style":            "CSS is not supported; put fill, stroke and font-* on the elements (or on a <g>)",
	"script":           "scripts are not allowed",
	"foreignObject":    "HTML inside SVG is not supported; use <text> with one <tspan> per line",
	"use":              "<use>/<symbol> are not supported; repeat the shapes or wrap them in <g> and copy it",
	"symbol":           "<use>/<symbol> are not supported; repeat the shapes",
	"clipPath":         "clipping is not supported; draw the shape at its final size",
	"mask":             "masks are not supported; use fill-opacity or a gradient",
	"pattern":          "patterns are not supported; use a solid colour or a gradient",
	"switch":           "<switch> is not supported",
	"a":                "links are not supported here",
	"textPath":         "text on a path is not supported; use <text> (optionally with rotate in transform)",
	"animate":          "animation is not supported",
	"animateTransform": "animation is not supported",
	"set":              "animation is not supported",
	"feGaussianBlur":   "only <feDropShadow> is supported inside <filter>",
	"feOffset":         "only <feDropShadow> is supported inside <filter>",
	"feBlend":          "only <feDropShadow> is supported inside <filter>",
	"feColorMatrix":    "only <feDropShadow> is supported inside <filter>",
	"feFlood":          "only <feDropShadow> is supported inside <filter>",
	"feMerge":          "only <feDropShadow> is supported inside <filter>",
	"feComposite":      "only <feDropShadow> is supported inside <filter>",
}

// ---- colours ------------------------------------------------------------

var svgNamedColors = map[string]string{
	"black": "000000", "white": "FFFFFF", "red": "FF0000", "green": "008000", "blue": "0000FF",
	"yellow": "FFFF00", "gray": "808080", "grey": "808080", "orange": "FFA500", "purple": "800080",
	"pink": "FFC0CB", "cyan": "00FFFF", "aqua": "00FFFF", "magenta": "FF00FF", "fuchsia": "FF00FF",
	"silver": "C0C0C0", "navy": "000080", "teal": "008080", "maroon": "800000", "lime": "00FF00",
	"olive": "808000", "brown": "A52A2A", "gold": "FFD700", "darkgray": "A9A9A9", "darkgrey": "A9A9A9",
	"lightgray": "D3D3D3", "lightgrey": "D3D3D3", "darkblue": "00008B", "lightblue": "ADD8E6",
	"orangered": "FF4500", "crimson": "DC143C", "indigo": "4B0082", "violet": "EE82EE",
	"skyblue": "87CEEB", "steelblue": "4682B4", "whitesmoke": "F5F5F5", "coral": "FF7F50",
	"tomato": "FF6347", "darkgreen": "006400", "lightgreen": "90EE90", "slategray": "708090",
	"dimgray": "696969", "dodgerblue": "1E90FF", "royalblue": "4169E1", "seagreen": "2E8B57",
}

// parseColor reads an SVG colour. none reports "no paint". alpha is 0..1.
func parseColor(s string) (rgb string, alpha float64, none bool, err error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch {
	case v == "" || v == "none" || v == "transparent":
		return "", 1, true, nil
	case strings.HasPrefix(v, "#"):
		h := v[1:]
		switch len(h) {
		case 3, 4:
			var b strings.Builder
			for _, c := range h {
				b.WriteRune(c)
				b.WriteRune(c)
			}
			h = b.String()
		}
		if len(h) != 6 && len(h) != 8 {
			return "", 1, false, fmt.Errorf("colour %q is not #rgb or #rrggbb", s)
		}
		if _, e := strconv.ParseUint(h, 16, 32); e != nil {
			return "", 1, false, fmt.Errorf("colour %q is not hexadecimal", s)
		}
		alpha = 1
		if len(h) == 8 {
			a, _ := strconv.ParseUint(h[6:], 16, 32)
			alpha = float64(a) / 255
		}
		return strings.ToUpper(h[:6]), alpha, false, nil
	case strings.HasPrefix(v, "rgb"):
		open, end := strings.IndexByte(v, '('), strings.LastIndexByte(v, ')')
		if open < 0 || end < open {
			return "", 1, false, fmt.Errorf("colour %q is not valid", s)
		}
		parts := strings.FieldsFunc(v[open+1:end], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(parts) < 3 {
			return "", 1, false, fmt.Errorf("colour %q needs three channels", s)
		}
		var ch [3]int
		for i := 0; i < 3; i++ {
			p := parts[i]
			if strings.HasSuffix(p, "%") {
				f, e := strconv.ParseFloat(strings.TrimSuffix(p, "%"), 64)
				if e != nil {
					return "", 1, false, fmt.Errorf("colour %q is not valid", s)
				}
				ch[i] = int(math.Round(f * 2.55))
			} else {
				f, e := strconv.ParseFloat(p, 64)
				if e != nil {
					return "", 1, false, fmt.Errorf("colour %q is not valid", s)
				}
				ch[i] = int(math.Round(f))
			}
			ch[i] = max(0, min(255, ch[i]))
		}
		alpha = 1
		if len(parts) >= 4 {
			p := parts[3]
			f, e := strconv.ParseFloat(strings.TrimSuffix(p, "%"), 64)
			if e != nil {
				return "", 1, false, fmt.Errorf("colour %q is not valid", s)
			}
			if strings.HasSuffix(p, "%") {
				f /= 100
			}
			alpha = math.Max(0, math.Min(1, f))
		}
		return fmt.Sprintf("%02X%02X%02X", ch[0], ch[1], ch[2]), alpha, false, nil
	}
	if h, ok := svgNamedColors[v]; ok {
		return h, 1, false, nil
	}
	if v == "currentcolor" {
		return "", 1, false, errors.New("currentColor is not supported; write the colour")
	}
	return "", 1, false, fmt.Errorf("colour %q is not supported (use #rrggbb, rgb(), or a basic colour name)", s)
}

// ---- paint and style ----------------------------------------------------

type svgStop struct {
	pos   float64 // 0..1
	rgb   string
	alpha float64
}

type svgGrad struct {
	radial         bool
	x1, y1, x2, y2 float64 // linear: fractions of the box (objectBoundingBox) or user units
	cx, cy         float64 // radial centre (fractions)
	userSpace      bool
	stops          []svgStop
}

type svgPaint struct {
	none  bool
	rgb   string
	alpha float64 // 1 = opaque
	grad  *svgGrad
}

var noPaint = svgPaint{none: true, alpha: 1}

func solidPaint(rgb string) svgPaint { return svgPaint{rgb: rgb, alpha: 1} }

// svgStyle is the inherited presentation state while walking the tree.
type svgStyle struct {
	fill, stroke           svgPaint
	strokeW                float64 // user units
	dash                   []float64
	cap, join              string
	opacity                float64
	fillOp, strokeOp       float64
	fontFamily             string
	fontSize               float64 // user units
	bold, italic, under    bool
	strike                 bool
	anchor                 string // start, middle, end
	baseline               string
	spacing                float64 // letter-spacing, user units
	filter                 string
	markerStart, markerEnd bool
}

func rootStyle() svgStyle {
	return svgStyle{
		fill: solidPaint("000000"), stroke: noPaint, strokeW: 1, opacity: 1, fillOp: 1, strokeOp: 1,
		fontSize: 16, anchor: "start", cap: "butt", join: "miter",
	}
}

// svgLength parses a number with an optional px/pt unit into user units.
func svgLength(s string) (float64, error) {
	v := strings.TrimSpace(s)
	scale := 1.0
	switch {
	case strings.HasSuffix(v, "px"):
		v = strings.TrimSuffix(v, "px")
	case strings.HasSuffix(v, "pt"):
		v = strings.TrimSuffix(v, "pt")
		scale = 4.0 / 3
	case strings.HasSuffix(v, "%"), strings.HasSuffix(v, "em"), strings.HasSuffix(v, "rem"), strings.HasSuffix(v, "mm"), strings.HasSuffix(v, "cm"), strings.HasSuffix(v, "in"):
		return 0, fmt.Errorf("%q: only plain numbers, px and pt are supported", s)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > maxSVGCoord {
		return 0, fmt.Errorf("%q is not a usable number", s)
	}
	return f * scale, nil
}
