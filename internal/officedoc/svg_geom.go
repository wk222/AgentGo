package officedoc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Geometry for the SVG slide pipeline: affine transforms, number scanning and
// path data. Path data is normalised to absolute M / L / C / Z segments, the
// only four things DrawingML's custGeom needs (quadratics and arcs are turned
// into cubic Béziers).

type pt struct{ x, y float64 }

// affine is the SVG matrix(a b c d e f): x' = a·x + c·y + e, y' = b·x + d·y + f.
type affine struct{ a, b, c, d, e, f float64 }

var identity = affine{1, 0, 0, 1, 0, 0}

// mul returns m·n: n is applied first, then m.
func (m affine) mul(n affine) affine {
	return affine{
		a: m.a*n.a + m.c*n.b, b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d, d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e, f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m affine) apply(p pt) pt {
	return pt{m.a*p.x + m.c*p.y + m.e, m.b*p.x + m.d*p.y + m.f}
}

func (m affine) det() float64 { return m.a*m.d - m.b*m.c }

// scaleFactor is the length scale of m (exact for similarities).
func (m affine) scaleFactor() float64 { return math.Sqrt(math.Abs(m.det())) }

// axisAligned reports whether m only scales and translates (no rotation or skew).
func (m affine) axisAligned() bool { return math.Abs(m.b) < 1e-9 && math.Abs(m.c) < 1e-9 }

// similarity reports whether m is a rotation plus a uniform scale (no mirror).
func (m affine) similarity() (scale, deg float64, ok bool) {
	s1, s2 := math.Hypot(m.a, m.b), math.Hypot(m.c, m.d)
	if s1 == 0 || math.Abs(s1-s2) > 1e-6*s1 {
		return 0, 0, false
	}
	if math.Abs(m.a*m.c+m.b*m.d) > 1e-6*s1*s2 || m.det() < 0 {
		return 0, 0, false
	}
	return s1, math.Atan2(m.b, m.a) * 180 / math.Pi, true
}

// scanner reads numbers out of SVG attribute text.
type scanner struct {
	s string
	i int
}

func (sc *scanner) skipSep() {
	for sc.i < len(sc.s) {
		switch sc.s[sc.i] {
		case ' ', '\t', '\r', '\n', ',':
			sc.i++
		default:
			return
		}
	}
}

func (sc *scanner) done() bool { sc.skipSep(); return sc.i >= len(sc.s) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// number reads the next number; "1.5.5" is 1.5 then .5, "1e2" is 100.
func (sc *scanner) number() (float64, bool) {
	sc.skipSep()
	s, i, start := sc.s, sc.i, sc.i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && isDigit(s[i]) {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	v, err := strconv.ParseFloat(s[start:i], 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) || math.Abs(v) > maxSVGCoord {
		return 0, false
	}
	sc.i = i
	return v, true
}

// maxSVGCoord bounds every coordinate, so conversions to EMU cannot overflow.
const maxSVGCoord = 1e6

// flag reads an arc flag: a single 0 or 1, which may touch the next number.
func (sc *scanner) flag() (bool, bool) {
	sc.skipSep()
	if sc.i < len(sc.s) && (sc.s[sc.i] == '0' || sc.s[sc.i] == '1') {
		v := sc.s[sc.i] == '1'
		sc.i++
		return v, true
	}
	return false, false
}

// numberList parses a list such as a points attribute or a transform argument list.
func numberList(s string) ([]float64, error) {
	sc := &scanner{s: s}
	var out []float64
	for !sc.done() {
		v, ok := sc.number()
		if !ok {
			return nil, fmt.Errorf("%q is not a list of numbers", truncateRunes(s, 40))
		}
		out = append(out, v)
	}
	return out, nil
}

// parseTransform parses an SVG transform list into one matrix.
func parseTransform(s string) (affine, error) {
	m := identity
	rest := strings.TrimSpace(s)
	for rest != "" {
		open, end := strings.IndexByte(rest, '('), strings.IndexByte(rest, ')')
		if open < 0 || end < open {
			return identity, fmt.Errorf("transform %q is not valid", truncateRunes(s, 60))
		}
		name := strings.TrimSpace(rest[:open])
		args, err := numberList(rest[open+1 : end])
		if err != nil {
			return identity, fmt.Errorf("transform %s: %w", name, err)
		}
		need := func(counts ...int) error {
			for _, c := range counts {
				if len(args) == c {
					return nil
				}
			}
			return fmt.Errorf("transform %s() takes %v argument(s), got %d", name, counts, len(args))
		}
		t := identity
		switch name {
		case "translate":
			if err := need(1, 2); err != nil {
				return identity, err
			}
			t.e = args[0]
			if len(args) == 2 {
				t.f = args[1]
			}
		case "scale":
			if err := need(1, 2); err != nil {
				return identity, err
			}
			t.a, t.d = args[0], args[0]
			if len(args) == 2 {
				t.d = args[1]
			}
		case "rotate":
			if err := need(1, 3); err != nil {
				return identity, err
			}
			rad := args[0] * math.Pi / 180
			sin, cos := math.Sincos(rad)
			t = affine{cos, sin, -sin, cos, 0, 0}
			if len(args) == 3 {
				cx, cy := args[1], args[2]
				t = affine{1, 0, 0, 1, cx, cy}.mul(t).mul(affine{1, 0, 0, 1, -cx, -cy})
			}
		case "matrix":
			if err := need(6); err != nil {
				return identity, err
			}
			t = affine{args[0], args[1], args[2], args[3], args[4], args[5]}
		case "skewX":
			if err := need(1); err != nil {
				return identity, err
			}
			t.c = math.Tan(args[0] * math.Pi / 180)
		case "skewY":
			if err := need(1); err != nil {
				return identity, err
			}
			t.b = math.Tan(args[0] * math.Pi / 180)
		default:
			return identity, fmt.Errorf("transform %q is not supported (use translate, scale, rotate or matrix)", name)
		}
		m = m.mul(t)
		rest = strings.TrimLeft(rest[end+1:], " ,\t\r\n")
	}
	return m, nil
}

// pathSeg is one normalised path segment. M and L use p[0]; C uses p[0..2]; Z uses none.
type pathSeg struct {
	op byte // 'M', 'L', 'C', 'Z'
	p  [3]pt
}

const maxPathSegs = 4000

// parsePathData normalises SVG path data to absolute M/L/C/Z segments.
func parsePathData(d string) ([]pathSeg, error) {
	sc := &scanner{s: d}
	var (
		segs        []pathSeg
		cur, start  pt
		lastCubic   pt // second control point of the previous C/S
		lastQuad    pt // control point of the previous Q/T
		prev        byte
		cmd         byte
		needMove    bool // a drawing command after Z starts at the subpath start
		movedOnce   bool
		emitMoveFix = func() {
			if needMove {
				segs = append(segs, pathSeg{op: 'M', p: [3]pt{cur}})
				needMove = false
			}
		}
	)
	args := func(n int) ([]float64, error) {
		out := make([]float64, n)
		for i := range out {
			v, ok := sc.number()
			if !ok {
				return nil, fmt.Errorf("command %c at offset %d needs %d numbers", cmd, sc.i, n)
			}
			out[i] = v
		}
		return out, nil
	}
	for {
		sc.skipSep()
		if sc.i >= len(sc.s) {
			break
		}
		if len(segs) > maxPathSegs {
			return nil, fmt.Errorf("path has more than %d segments; simplify it", maxPathSegs)
		}
		ch := sc.s[sc.i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			cmd = ch
			sc.i++
		} else if cmd == 0 || cmd == 'z' || cmd == 'Z' {
			return nil, fmt.Errorf("unexpected %q at offset %d", string(ch), sc.i)
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		if !movedOnce && up != 'M' {
			return nil, fmt.Errorf("path data must start with M (found %c)", cmd)
		}
		off := func(p pt) pt {
			if rel {
				return pt{p.x + cur.x, p.y + cur.y}
			}
			return p
		}
		switch up {
		case 'Z':
			segs = append(segs, pathSeg{op: 'Z'})
			cur = start
			needMove = true
			prev = 'Z'
			continue
		case 'M':
			a, err := args(2)
			if err != nil {
				return nil, err
			}
			p := off(pt{a[0], a[1]})
			segs = append(segs, pathSeg{op: 'M', p: [3]pt{p}})
			cur, start = p, p
			movedOnce, needMove = true, false
			prev = 'M'
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
			continue
		case 'L':
			a, err := args(2)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			p := off(pt{a[0], a[1]})
			segs = append(segs, pathSeg{op: 'L', p: [3]pt{p}})
			cur = p
		case 'H':
			a, err := args(1)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			p := cur
			if rel {
				p.x += a[0]
			} else {
				p.x = a[0]
			}
			segs = append(segs, pathSeg{op: 'L', p: [3]pt{p}})
			cur = p
		case 'V':
			a, err := args(1)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			p := cur
			if rel {
				p.y += a[0]
			} else {
				p.y = a[0]
			}
			segs = append(segs, pathSeg{op: 'L', p: [3]pt{p}})
			cur = p
		case 'C':
			a, err := args(6)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			c1, c2, p := off(pt{a[0], a[1]}), off(pt{a[2], a[3]}), off(pt{a[4], a[5]})
			segs = append(segs, pathSeg{op: 'C', p: [3]pt{c1, c2, p}})
			lastCubic, cur = c2, p
		case 'S':
			a, err := args(4)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			c1 := cur
			if prev == 'C' || prev == 'S' {
				c1 = pt{2*cur.x - lastCubic.x, 2*cur.y - lastCubic.y}
			}
			c2, p := off(pt{a[0], a[1]}), off(pt{a[2], a[3]})
			segs = append(segs, pathSeg{op: 'C', p: [3]pt{c1, c2, p}})
			lastCubic, cur = c2, p
		case 'Q':
			a, err := args(4)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			q, p := off(pt{a[0], a[1]}), off(pt{a[2], a[3]})
			segs = append(segs, quadToCubic(cur, q, p))
			lastQuad, cur = q, p
		case 'T':
			a, err := args(2)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			q := cur
			if prev == 'Q' || prev == 'T' {
				q = pt{2*cur.x - lastQuad.x, 2*cur.y - lastQuad.y}
			}
			p := off(pt{a[0], a[1]})
			segs = append(segs, quadToCubic(cur, q, p))
			lastQuad, cur = q, p
		case 'A':
			a, err := args(3)
			if err != nil {
				return nil, err
			}
			fa, ok1 := sc.flag()
			fs, ok2 := sc.flag()
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("arc at offset %d needs two 0/1 flags", sc.i)
			}
			e, err := args(2)
			if err != nil {
				return nil, err
			}
			emitMoveFix()
			p := off(pt{e[0], e[1]})
			segs = append(segs, arcToCubics(cur, a[0], a[1], a[2], fa, fs, p)...)
			cur = p
		default:
			return nil, fmt.Errorf("unknown path command %q", string(cmd))
		}
		prev = up
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("path data is empty")
	}
	return segs, nil
}

func quadToCubic(p0, q, p pt) pathSeg {
	return pathSeg{op: 'C', p: [3]pt{
		{p0.x + 2.0/3*(q.x-p0.x), p0.y + 2.0/3*(q.y-p0.y)},
		{p.x + 2.0/3*(q.x-p.x), p.y + 2.0/3*(q.y-p.y)},
		p,
	}}
}

// arcToCubics converts an SVG elliptical arc (endpoint form) to cubic Béziers
// of at most 90 degrees each (SVG 1.1 appendix F.6).
func arcToCubics(p0 pt, rx, ry, rotDeg float64, large, sweep bool, p pt) []pathSeg {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if p0 == p {
		return nil
	}
	if rx == 0 || ry == 0 {
		return []pathSeg{{op: 'L', p: [3]pt{p}}}
	}
	phi := rotDeg * math.Pi / 180
	sinP, cosP := math.Sincos(phi)
	dx, dy := (p0.x-p.x)/2, (p0.y-p.y)/2
	x1 := cosP*dx + sinP*dy
	y1 := -sinP*dx + cosP*dy
	if l := x1*x1/(rx*rx) + y1*y1/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	coef := 0.0
	if den != 0 {
		coef = math.Sqrt(math.Max(0, num/den))
	}
	if large == sweep {
		coef = -coef
	}
	cxp, cyp := coef*rx*y1/ry, -coef*ry*x1/rx
	cx := cosP*cxp - sinP*cyp + (p0.x+p.x)/2
	cy := sinP*cxp + cosP*cyp + (p0.y+p.y)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	th1 := ang(1, 0, (x1-cxp)/rx, (y1-cyp)/ry)
	dth := ang((x1-cxp)/rx, (y1-cyp)/ry, (-x1-cxp)/rx, (-y1-cyp)/ry)
	if !sweep && dth > 0 {
		dth -= 2 * math.Pi
	} else if sweep && dth < 0 {
		dth += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dth)/(math.Pi/2) - 1e-9))
	if n < 1 {
		n = 1
	}
	delta := dth / float64(n)
	t := 4.0 / 3 * math.Tan(delta/4)
	at := func(u pt) pt { // unit circle point -> ellipse point
		x, y := u.x*rx, u.y*ry
		return pt{cosP*x - sinP*y + cx, sinP*x + cosP*y + cy}
	}
	var out []pathSeg
	a := th1
	for i := 0; i < n; i++ {
		s1, c1 := math.Sincos(a)
		s2, c2 := math.Sincos(a + delta)
		seg := pathSeg{op: 'C', p: [3]pt{
			at(pt{c1 - t*s1, s1 + t*c1}),
			at(pt{c2 + t*s2, s2 - t*c2}),
			at(pt{c2, s2}),
		}}
		if i == n-1 {
			seg.p[2] = p // land exactly on the requested end point
		}
		out = append(out, seg)
		a += delta
	}
	return out
}

// ellipseSegs draws an axis-aligned ellipse as four cubic Béziers.
func ellipseSegs(cx, cy, rx, ry float64) []pathSeg {
	const k = 0.5522847498307936
	return []pathSeg{
		{op: 'M', p: [3]pt{{cx + rx, cy}}},
		{op: 'C', p: [3]pt{{cx + rx, cy + k*ry}, {cx + k*rx, cy + ry}, {cx, cy + ry}}},
		{op: 'C', p: [3]pt{{cx - k*rx, cy + ry}, {cx - rx, cy + k*ry}, {cx - rx, cy}}},
		{op: 'C', p: [3]pt{{cx - rx, cy - k*ry}, {cx - k*rx, cy - ry}, {cx, cy - ry}}},
		{op: 'C', p: [3]pt{{cx + k*rx, cy - ry}, {cx + rx, cy - k*ry}, {cx + rx, cy}}},
		{op: 'Z'},
	}
}

// transformSegs applies m to every point.
func transformSegs(segs []pathSeg, m affine) []pathSeg {
	out := make([]pathSeg, len(segs))
	for i, s := range segs {
		out[i].op = s.op
		for j := range s.p {
			out[i].p[j] = m.apply(s.p[j])
		}
	}
	return out
}

// segBounds returns the exact bounding box of the segments (cubic extrema included).
func segBounds(segs []pathSeg) (minX, minY, maxX, maxY float64, ok bool) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	add := func(p pt) {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
		ok = true
	}
	var cur pt
	for _, s := range segs {
		switch s.op {
		case 'M', 'L':
			add(s.p[0])
			cur = s.p[0]
		case 'C':
			add(s.p[2])
			for axis := 0; axis < 2; axis++ {
				get := func(p pt) float64 {
					if axis == 0 {
						return p.x
					}
					return p.y
				}
				v0, v1, v2, v3 := get(cur), get(s.p[0]), get(s.p[1]), get(s.p[2])
				d0, d1, d2 := v1-v0, v2-v1, v3-v2
				a, b, c := d0-2*d1+d2, 2*(d1-d0), d0
				for _, t := range quadRoots(a, b, c) {
					if t <= 0 || t >= 1 {
						continue
					}
					u := 1 - t
					v := u*u*u*v0 + 3*u*u*t*v1 + 3*u*t*t*v2 + t*t*t*v3
					if axis == 0 {
						add(pt{v, cur.y})
					} else {
						add(pt{cur.x, v})
					}
				}
			}
			cur = s.p[2]
		}
	}
	return minX, minY, maxX, maxY, ok
}

func quadRoots(a, b, c float64) []float64 {
	if math.Abs(a) < 1e-12 {
		if math.Abs(b) < 1e-12 {
			return nil
		}
		return []float64{-c / b}
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return nil
	}
	sq := math.Sqrt(disc)
	return []float64{(-b + sq) / (2 * a), (-b - sq) / (2 * a)}
}
