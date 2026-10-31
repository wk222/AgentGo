package officedoc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func ops(segs []pathSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte(s.op)
	}
	return b.String()
}

func TestParsePathDataNormalisesCommands(t *testing.T) {
	segs, err := parsePathData("M10 10 h20 v10 H0 z")
	if err != nil || ops(segs) != "MLLLZ" {
		t.Fatalf("ops = %q err = %v", ops(segs), err)
	}
	if segs[1].p[0] != (pt{30, 10}) || segs[2].p[0] != (pt{30, 20}) || segs[3].p[0] != (pt{0, 20}) {
		t.Errorf("relative/absolute lines wrong: %+v", segs)
	}

	segs, _ = parsePathData("M0 0 10 10 20 0")
	if ops(segs) != "MLL" {
		t.Errorf("implicit lineto: %q", ops(segs))
	}

	// A quadratic keeps its shape when raised to a cubic: the midpoint stays put.
	segs, err = parsePathData("M0 0 Q50 100 100 0")
	if err != nil || ops(segs) != "MC" {
		t.Fatal(ops(segs), err)
	}
	c := segs[1].p
	mid := 0.125*0 + 0.375*c[0].y + 0.375*c[1].y + 0.125*0
	if !near(mid, 50, 1e-9) {
		t.Errorf("quadratic midpoint y = %v, want 50", mid)
	}

	// Smooth commands reflect the previous control point.
	segs, _ = parsePathData("M0 0 C 0 10 10 10 10 0 S 20 -10 20 0")
	if !near(segs[2].p[0].y, -10, 1e-9) || !near(segs[2].p[0].x, 10, 1e-9) {
		t.Errorf("S reflection = %+v", segs[2].p[0])
	}
}

func TestPathArcsBecomeCubicsOnTheCircle(t *testing.T) {
	// Flags written without separators, the way minified SVG does.
	for _, d := range []string{"M0 0a5 5 0 01 10 0", "M0 0A5 5 0 0 1 10 0", "M0 0a5,5,0,0,1,10,0"} {
		segs, err := parsePathData(d)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if len(segs) < 3 || ops(segs)[1] != 'C' {
			t.Fatalf("%s: ops %q", d, ops(segs))
		}
		cur := segs[0].p[0]
		for _, s := range segs[1:] {
			// Sample the Bézier: every point must sit 5 units from the centre (5, 0).
			for _, tt := range []float64{0.25, 0.5, 0.75, 1} {
				u := 1 - tt
				x := u*u*u*cur.x + 3*u*u*tt*s.p[0].x + 3*u*tt*tt*s.p[1].x + tt*tt*tt*s.p[2].x
				y := u*u*u*cur.y + 3*u*u*tt*s.p[0].y + 3*u*tt*tt*s.p[1].y + tt*tt*tt*s.p[2].y
				if r := math.Hypot(x-5, y); !near(r, 5, 0.01) {
					t.Errorf("%s: point (%.3f,%.3f) is %.4f from the centre", d, x, y, r)
				}
			}
			cur = s.p[2]
		}
		if cur != (pt{10, 0}) {
			t.Errorf("%s: arc ends at %+v", d, cur)
		}
		// sweep=1 from (0,0) to (10,0) goes over the top: y is negative on the way.
		_, minY, _, _, _ := segBounds(segs)
		if !near(minY, -5, 0.01) {
			t.Errorf("%s: arc top = %v, want -5", d, minY)
		}
	}
	// A radius that is too small is scaled up until the arc fits; a zero radius is a line.
	segs, _ := parsePathData("M0 0 A1 1 0 0 1 10 0")
	if _, minY, _, _, _ := segBounds(segs); !near(minY, -5, 0.01) {
		t.Errorf("scaled radius arc top = %v", minY)
	}
	segs, _ = parsePathData("M0 0 A0 5 0 0 1 10 0")
	if ops(segs) != "ML" {
		t.Errorf("zero radius: %q", ops(segs))
	}
}

func TestParsePathDataRejectsNonsense(t *testing.T) {
	for d, want := range map[string]string{
		"L10 10":              "must start with M",
		"M0":                  "needs 2 numbers",
		"M0 0 X 1":            "unknown path command",
		"":                    "empty",
		"M0 0 z 5 5":          "unexpected",
		"M0 0 A5 5 0 2 1 9 9": "flags",
	} {
		if _, err := parsePathData(d); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", d, err, want)
		}
	}
}

func TestSegBoundsUsesCurveExtrema(t *testing.T) {
	segs, _ := parsePathData("M0 0 C 0 100 100 100 100 0")
	x0, y0, x1, y1, ok := segBounds(segs)
	if !ok || x0 != 0 || x1 != 100 || y0 != 0 || !near(y1, 75, 1e-9) {
		t.Errorf("bounds = %v %v %v %v", x0, y0, x1, y1)
	}
}

func TestTransformsCompose(t *testing.T) {
	m, err := parseTransform("translate(10 20) scale(2)")
	if err != nil || m.apply(pt{1, 1}) != (pt{12, 22}) {
		t.Fatalf("m = %+v err = %v", m, err)
	}
	r, _ := parseTransform("rotate(90)")
	if p := r.apply(pt{1, 0}); !near(p.x, 0, 1e-9) || !near(p.y, 1, 1e-9) {
		t.Errorf("rotate(90) of (1,0) = %+v", p)
	}
	if s, deg, ok := r.similarity(); !ok || !near(s, 1, 1e-9) || !near(deg, 90, 1e-9) {
		t.Errorf("similarity = %v %v %v", s, deg, ok)
	}
	// Rotation about a point leaves that point where it is.
	ra, _ := parseTransform("rotate(45 100 50)")
	if p := ra.apply(pt{100, 50}); !near(p.x, 100, 1e-9) || !near(p.y, 50, 1e-9) {
		t.Errorf("rotate about a point moved it: %+v", p)
	}
	sk, _ := parseTransform("skewX(20)")
	if _, _, ok := sk.similarity(); ok || sk.axisAligned() {
		t.Error("a skew is neither a similarity nor axis aligned")
	}
	if _, err := parseTransform("wobble(3)"); err == nil {
		t.Error("unknown transform accepted")
	}
	if _, err := parseTransform("translate(1 2 3)"); err == nil {
		t.Error("wrong argument count accepted")
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[string]string{"#abc": "AABBCC", "#1f3a5f": "1F3A5F", "rgb(255, 0, 128)": "FF0080", "red": "FF0000", "RGB(0%,100%,0%)": "00FF00"} {
		got, _, none, err := parseColor(in)
		if err != nil || none || got != want {
			t.Errorf("%s -> %q none=%v err=%v, want %s", in, got, none, err, want)
		}
	}
	if _, a, _, _ := parseColor("rgba(0,0,0,0.25)"); !near(a, 0.25, 1e-9) {
		t.Errorf("rgba alpha = %v", a)
	}
	if _, a, _, _ := parseColor("#ff000080"); !near(a, 128.0/255, 1e-9) {
		t.Errorf("#rrggbbaa alpha = %v", a)
	}
	for _, in := range []string{"none", "transparent", ""} {
		if _, _, none, err := parseColor(in); !none || err != nil {
			t.Errorf("%q should be no paint", in)
		}
	}
	for _, in := range []string{"currentColor", "#12", "#gggggg", "rgb(1,2)", "chartreuse-ish"} {
		if _, _, _, err := parseColor(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func testDeck() *deck {
	deny := func(rel string) (string, error) { return "", errors.New("path escapes the workspace: " + rel) }
	return &deck{font: defaultFont, pal: palettes["blue"], env: Env{Resolve: deny}}
}

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 720">`

func TestSVGTextLayoutMatchesTheBaseline(t *testing.T) {
	// Centre-anchored two-line text: baseline of line 1 at y=300 (size 32px), line 2 at 360.
	sc, err := testDeck().convertSVG(svgHead + `<text x="640" y="300" font-size="32" font-weight="bold" fill="#112233" text-anchor="middle">` +
		`<tspan x="640" y="300">第一行</tspan><tspan x="640" y="360">Second 行</tspan></text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.nodes) != 1 || sc.nodes[0].kind != "text" || len(sc.nodes[0].lines) != 2 {
		t.Fatalf("nodes = %+v", sc.nodes)
	}
	nd := sc.nodes[0]
	k := slideW / 1280
	if !near(nd.x+nd.w/2, slideW/2, 1e-6) {
		t.Errorf("centre-anchored text is centred at %v, want %v", nd.x+nd.w/2, slideW/2)
	}
	// Baselines are 60px apart and the lines are the same size: one uniform pitch of 60px,
	// and the first baseline sits svgFitK*60 - svgFitS*32 below the top of the box.
	wantPitch := 60 * k * ptPerInch
	if !near(nd.lines[0].pitch, wantPitch, 1e-6) || !near(nd.lines[1].pitch, wantPitch, 1e-6) {
		t.Errorf("pitches = %v %v pt, want %v", nd.lines[0].pitch, nd.lines[1].pitch, wantPitch)
	}
	if want := (300 - (svgFitK*60 - svgFitS*32)) * k; !near(nd.y, want, 1e-6) {
		t.Errorf("top = %v, want %v", nd.y, want)
	}
	if want := 120 * k; !near(nd.h, want, 1e-6) {
		t.Errorf("height = %v, want %v", nd.h, want)
	}
	// With a larger second line the pitch adjusts so the baselines still land 60px apart
	// under the midpoint model.
	sc2, err := testDeck().convertSVG(svgHead + `<text><tspan x="10" y="100" font-size="20">a</tspan><tspan x="10" y="160" font-size="40">b</tspan></text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	n2 := sc2.nodes[0]
	p0, p1 := n2.lines[0].pitch/(k*ptPerInch), n2.lines[1].pitch/(k*ptPerInch)
	top := n2.y / k
	b0 := top + svgFitK*p0 - svgFitS*20
	b1 := top + p0 + svgFitK*p1 - svgFitS*40
	if !near(b0, 100, 1e-6) || !near(b1, 160, 1e-6) {
		t.Errorf("mixed sizes: baselines at %v and %v, want 100 and 160", b0, b1)
	}
	r := nd.lines[0].runs[0]
	if want := 32 * k * ptPerInch; !near(r.size, want, 1e-9) || !r.bold || r.rgb != "112233" || r.text != "第一行" {
		t.Errorf("run = %+v (size want %v)", r, want)
	}
}

func TestSVGTextSplitsWhenLinesHaveDifferentLeftEdges(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<text font-size="20"><tspan x="100" y="100">A</tspan><tspan x="100" y="130">B</tspan><tspan x="400" y="160">C</tspan></text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.nodes) != 2 || len(sc.nodes[0].lines) != 2 || len(sc.nodes[1].lines) != 1 {
		t.Fatalf("expected a 2-line box and a 1-line box, got %d nodes", len(sc.nodes))
	}
}

func TestSVGInlineRunsAndWhitespace(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<text x="10" y="50" font-size="20">
		  Total <tspan font-weight="bold" fill="#ff0000">  98.6%  </tspan>   done
	</text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	runs := sc.nodes[0].lines[0].runs
	var texts []string
	for _, r := range runs {
		texts = append(texts, r.text)
	}
	if strings.Join(texts, "|") != "Total |98.6% |done" || !runs[1].bold || runs[1].rgb != "FF0000" || runs[0].bold {
		t.Errorf("runs = %q %+v", texts, runs)
	}
}

func TestSVGTransformsFlattenIntoSlideCoordinates(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<g transform="translate(100 50) scale(2)"><rect x="10" y="10" width="40" height="20" fill="#000"/></g></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	k := slideW / 1280
	r := sc.nodes[0]
	if !near(r.x, 120*k, 1e-9) || !near(r.y, 70*k, 1e-9) || !near(r.w, 80*k, 1e-9) || !near(r.h, 40*k, 1e-9) {
		t.Errorf("rect = %+v", r)
	}
	// A rotated rectangle keeps its size and gains a rotation about its centre.
	sc, err = testDeck().convertSVG(svgHead + `<rect x="100" y="100" width="200" height="100" transform="rotate(30 200 150)" fill="#000"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	r = sc.nodes[0]
	if !near(r.rot, 30, 1e-9) || !near(r.x+r.w/2, 200*k, 1e-9) || !near(r.y+r.h/2, 150*k, 1e-9) || !near(r.w, 200*k, 1e-9) {
		t.Errorf("rotated rect = %+v", r)
	}
}

func TestSVGFullSlideRectBecomesTheBackground(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<rect width="1280" height="720" fill="#F5F7FA"/><circle cx="640" cy="360" r="50" fill="red"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.nodes) != 1 || sc.nodes[0].kind != "ellipse" {
		t.Errorf("nodes = %d, want the circle only", len(sc.nodes))
	}
	if !strings.Contains(sc.bg, `<a:srgbClr val="F5F7FA"/>`) || !strings.HasPrefix(sc.bg, "<p:bg>") {
		t.Errorf("bg = %s", sc.bg)
	}
}

func TestSVGThemeColourTokens(t *testing.T) {
	d := testDeck()
	sc, err := d.convertSVG(svgHead + `<rect x="0" y="0" width="100" height="50" fill="primary" stroke="accent" stroke-width="2"/>` +
		`<text x="10" y="90" font-size="20" fill="MUTED">hi</text>` +
		`<linearGradient id="g"><stop offset="0" stop-color="chart1"/><stop offset="1" stop-color="chart2"/></linearGradient>` +
		`<rect x="0" y="100" width="100" height="50" fill="url(#g)"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	pal := d.pal
	if sc.nodes[0].fill.rgb != pal.Primary || sc.nodes[0].stroke.rgb != pal.Accent {
		t.Errorf("rect colours = %+v %+v, want %s %s", sc.nodes[0].fill, sc.nodes[0].stroke, pal.Primary, pal.Accent)
	}
	if got := sc.nodes[1].lines[0].runs[0].rgb; got != pal.Muted {
		t.Errorf("text colour = %s, want %s", got, pal.Muted)
	}
	if g := sc.nodes[2].fill.grad; g == nil || g.stops[0].rgb != pal.Chart[0] || g.stops[1].rgb != pal.Chart[1] {
		t.Errorf("gradient = %+v", g)
	}
	// An unknown name explains what is available.
	_, err = d.convertSVG(svgHead + `<rect width="9" height="9" fill="brand"/></svg>`)
	if err == nil || !strings.Contains(err.Error(), "primary, secondary, accent") {
		t.Errorf("err = %v", err)
	}
	// PowerPoint accepts 2 to 10 gradient stops.
	var stops strings.Builder
	for i := 0; i < 11; i++ {
		fmt.Fprintf(&stops, `<stop offset="%g" stop-color="#000"/>`, float64(i)/10)
	}
	_, err = d.convertSVG(svgHead + `<linearGradient id="g">` + stops.String() + `</linearGradient><rect width="9" height="9" fill="url(#g)"/></svg>`)
	if err == nil || !strings.Contains(err.Error(), "2 to 10") {
		t.Errorf("11 stops: %v", err)
	}
}

// The example in the skill is what models copy, so it has to convert cleanly.
func TestPptxSkillExampleConverts(t *testing.T) {
	text := skillText(t, "office-pptx")
	start := strings.Index(text, "<svg ")
	end := strings.Index(text[start:], "</svg>")
	if start < 0 || end < 0 {
		t.Fatal("the skill has no SVG example")
	}
	sc, err := testDeck().convertSVG(text[start : start+end+len("</svg>")])
	if err != nil {
		t.Fatalf("the example in the skill does not convert: %v", err)
	}
	var titles, groups, lines int
	for _, n := range sc.nodes {
		switch {
		case n.kind == "text" && n.title:
			titles++
		case n.kind == "group":
			groups++
		case n.kind == "line" && n.arrowEnd:
			lines++
		}
	}
	if titles != 1 || groups != 1 || lines != 1 || sc.bg == "" || len(sc.warnings) != 0 {
		t.Errorf("titles=%d groups=%d arrows=%d bg=%q warnings=%v", titles, groups, lines, sc.bg, sc.warnings)
	}
}

func TestSVGRotatedTextAndDataURIImage(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<text x="300" y="300" font-size="24" transform="rotate(-90 300 300)">纵向</text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	if n := sc.nodes[0]; !near(n.rot, -90, 1e-9) || n.h <= 0 {
		t.Errorf("rotated text = %+v", n)
	}
	if _, err := testDeck().convertSVG(svgHead + `<text x="1" y="1" transform="skewX(20)">x</text></svg>`); err == nil {
		t.Error("skewed text accepted")
	}

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	sc, err = testDeck().convertSVG(svgHead + `<image href="` + uri + `" x="100" y="100" width="400" height="400"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	// A 4x2 picture inside a 400x400 box: fitted to 400x200 and centred vertically.
	k := slideW / 1280
	if n := sc.nodes[0]; n.kind != "image" || n.imgExt != ".png" || !near(n.w, 400*k, 1e-9) || !near(n.h, 200*k, 1e-9) || !near(n.y, 200*k, 1e-9) {
		t.Errorf("image = %+v", n)
	}
	if _, err := testDeck().convertSVG(svgHead + `<image href="data:image/gif;base64,AAAA" width="9" height="9"/></svg>`); err == nil {
		t.Error("a gif data URI was accepted")
	}
}

func TestSVGTextWarnings(t *testing.T) {
	sc, err := testDeck().convertSVG(svgHead + `<text x="1200" y="100" font-size="24">这是一段很长的文字会超出页面右边缘</text>
<text x="10" y="200" font-size="20">first line
second line</text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(sc.warnings, "\n")
	for _, want := range []string{"runs off the slide", "line break inside <text>"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning containing %q in:\n%s", want, joined)
		}
	}
}

// Whatever a model emits, conversion must return an error or a scene, never panic.
func TestSVGGarbageDoesNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const alphabet = "MmLlHhVvCcSsQqTtAaZz0123456789.,- eE+"
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for n := rng.Intn(60); n >= 0; n-- {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		segs, err := parsePathData(b.String())
		if err == nil {
			// The writer must cope with whatever the parser accepted.
			sc, err := testDeck().convertSVG(svgHead + `<path d="` + b.String() + `" fill="#000" stroke="#000"/></svg>`)
			if err == nil {
				s := (&deck{font: defaultFont, pal: palettes["blue"]}).newSlide(layoutTitleOnly)
				s.writeNodes(sc.nodes, true)
				wellFormed(t, "garbage path", `<r xmlns:a="a" xmlns:p="p" xmlns:r="r">`+s.shapes.String()+`</r>`)
			}
			_, _, _, _, _ = segBounds(segs)
		}
	}
	for _, src := range []string{
		svgHead + `<rect width="1e308" height="5"/></svg>`,
		svgHead + `<rect width="NaN" height="5"/></svg>`,
		svgHead + `<circle r="-4"/></svg>`,
		svgHead + `<polygon points="1,2,3"/></svg>`,
		svgHead + `<text x="5" y="5" font-size="0">x</text></svg>`,
		svgHead + `<g transform="scale(0)"><rect width="10" height="10"/></g></svg>`,
		svgHead + `<g transform="matrix(1 2 3)"><rect width="10" height="10"/></g></svg>`,
		svgHead + `<text x="1" y="1"><tspan><tspan x="3" y="4">a</tspan></tspan></text></svg>`,
		svgHead + `<linearGradient id="a" href="#a"/><rect width="5" height="5" fill="url(#a)"/></svg>`,
		svgHead + `<rect width="5" height="5" style="fill:;stroke:;"/></svg>`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %.80s: %v", src, r)
				}
			}()
			_, _ = testDeck().convertSVG(src)
		}()
	}
}

func TestSVGReportsEveryProblemAtOnce(t *testing.T) {
	_, err := testDeck().convertSVG(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 720">
<style>.a{fill:red}</style>
<use href="#x"/>
<rect x="0" y="0" width="10" height="10" fill="chartreuse-ish" clip-path="url(#c)"/>
<path d="M0 0 L"/>
<text x="1 2 3" y="5">hi</text>
<foo/>
</svg>`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"<style>", "<use>", "chartreuse-ish", "clip-path", "d:", "per-character", "<foo>", "line 2", "line 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestSVGRejectsBadDocuments(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"empty":       {"", "empty"},
		"not xml":     {"<svg><rect></svg>", "well-formed"},
		"doctype":     {`<!DOCTYPE svg [<!ENTITY x "y">]><svg viewBox="0 0 1280 720"/>`, "DOCTYPE"},
		"wrong root":  {`<html/>`, "root element"},
		"no viewbox":  {`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`, "viewBox"},
		"not 16:9":    {`<svg viewBox="0 0 800 600"><rect width="1" height="1"/></svg>`, "16:9"},
		"draws none":  {`<svg viewBox="0 0 1280 720"></svg>`, "draws nothing"},
		"bad gradref": {`<svg viewBox="0 0 1280 720"><rect width="9" height="9" fill="url(#nope)"/></svg>`, "#nope"},
		"nested svg":  {`<svg viewBox="0 0 1280 720"><svg><rect width="1" height="1"/></svg></svg>`, "nested"},
	} {
		if _, err := testDeck().convertSVG(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := testDeck().convertSVG(svgHead + strings.Repeat(`<rect width="1" height="1"/>`, maxSVGShapes+1) + `</svg>`); err == nil || !strings.Contains(err.Error(), "shapes") {
		t.Errorf("shape cap: %v", err)
	}
	if _, err := testDeck().convertSVG(svgHead + strings.Repeat("<g>", maxSVGDepth+2) + strings.Repeat("</g>", maxSVGDepth+2) + `</svg>`); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("depth cap: %v", err)
	}
}

const svgSlideSample = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 720">
  <defs>
    <linearGradient id="hero" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#1F3A5F"/><stop offset="100%" stop-color="#2F6DB5" stop-opacity="0.9"/></linearGradient>
    <radialGradient id="glow"><stop offset="0" stop-color="#FFFFFF"/><stop offset="1" stop-color="#2F6DB5"/></radialGradient>
    <marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0 0 L9 3 L0 6 z"/></marker>
    <filter id="soft"><feDropShadow dx="0" dy="4" stdDeviation="6" flood-color="#000000" flood-opacity="0.25"/></filter>
  </defs>
  <rect width="1280" height="720" fill="#F5F7FA"/>
  <text id="title" x="64" y="96" font-size="40" font-weight="bold" fill="#1F3A5F">三季度故障闭环流程</text>
  <g id="steps">
    <rect x="64" y="160" width="300" height="140" rx="16" fill="url(#hero)" filter="url(#soft)"/>
    <text x="214" y="240" font-size="28" fill="#FFFFFF" text-anchor="middle">发现<tspan x="214" y="280" font-size="18">告警自动派单</tspan></text>
    <ellipse cx="640" cy="230" rx="110" ry="70" fill="url(#glow)" stroke="#2F6DB5" stroke-width="2" stroke-dasharray="6 4"/>
    <polygon points="900,160 1100,230 900,300" fill="#F28C28"/>
  </g>
  <line x1="364" y1="230" x2="520" y2="230" stroke="#6B7280" stroke-width="3" marker-end="url(#arrow)"/>
  <path d="M100 500 C 200 400 300 600 400 500 S 600 600 700 500 a 50 50 0 0 1 100 0" fill="none" stroke="#3FA796" stroke-width="4" stroke-linecap="round"/>
  <path d="M900 480 h200 a40 40 0 0 1 40 40 v80 h-240 z" fill="#8E6CC7" fill-opacity="0.5"/>
  <text x="64" y="660" font-size="18" fill="#6B7280">数据来源：工单系统</text>
</svg>`

func TestCreatePptxWithSVGSlide(t *testing.T) {
	root, ts := newXlsxTools(t)
	raw, err := run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "decks/svg.pptx", "title": "SVG 测试", "no_cover": true,
		"slides": []map[string]any{
			{"layout": "svg", "title": "三季度故障闭环流程", "svg": svgSlideSample, "notes": "讲稿"},
			{"layout": "bullets", "title": "普通页仍可混用", "bullets": []string{"一", "二"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out createPptxOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Slides != 2 || len(out.Warnings) != 0 || !strings.Contains(out.Outline[0], "三季度故障闭环流程") {
		t.Errorf("out = %+v", out)
	}
	parts := checkPackage(t, mustRead(t, filepath.Join(root, "decks", "svg.pptx")))
	s1 := parts["ppt/slides/slide1.xml"]
	for _, want := range []string{
		`<p:bg><p:bgPr><a:solidFill><a:srgbClr val="F5F7FA"/>`, // full-slide rect became the background
		`<p:ph type="title"/>`,                                 // id="title" is the title placeholder
		`<p:grpSp>`, `name="steps"`,
		`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val 11429"/>`, // rx 16 of min(300,140) -> 16/140
		`<a:gradFill rotWithShape="1">`, `<a:path path="circle">`,
		`<a:outerShdw`,
		`<a:prstDash val="sysDash"/>`,
		`<p:cxnSp>`, `<a:tailEnd type="triangle"`,
		`<a:custGeom>`, `<a:cubicBezTo>`, `cap="rnd"`,
		`fill="none"`,
		`<a:alpha val="50000"/>`,
		`algn="ctr"`,
		`三季度故障闭环流程`, `告警自动派单`, `数据来源：工单系统`,
	} {
		if !strings.Contains(s1, want) {
			t.Errorf("slide 1 lacks %s", want)
		}
	}
	if strings.Contains(s1, "<p:pic>") {
		t.Error("an SVG slide must not contain a picture")
	}
	if !strings.Contains(parts["ppt/notesSlides/notesSlide1.xml"], "讲稿") {
		t.Error("notes lost")
	}
	// The heading is not an off-slide hack: the title placeholder sits where the SVG put it.
	if !strings.Contains(s1, `<a:off x="`) || strings.Contains(s1, `x="-`) {
		t.Errorf("unexpected negative offsets in slide 1")
	}
}

func TestCreatePptxSVGSlideErrorsNameTheSlide(t *testing.T) {
	_, ts := newXlsxTools(t)
	_, err := run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "decks/bad.pptx", "title": "x",
		"slides": []map[string]any{{"layout": "svg", "title": "t", "svg": svgHead + `<use href="#a"/></svg>`}},
	})
	if err == nil || !strings.Contains(err.Error(), "slides[0] (svg)") || !strings.Contains(err.Error(), "<use>") {
		t.Errorf("err = %v", err)
	}
	_, err = run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "decks/bad2.pptx", "title": "x",
		"slides": []map[string]any{{"layout": "svg", "title": "t"}},
	})
	if err == nil || !strings.Contains(err.Error(), "svg is required") {
		t.Errorf("err = %v", err)
	}
}

func TestSVGImageFromWorkspaceAndDataURI(t *testing.T) {
	root, ts := newXlsxTools(t)
	writeTestPNG(t, root)
	_, err := run(t, ToolCreatePptx, ts, map[string]any{
		"relative_path": "decks/img.pptx", "title": "x", "no_cover": true,
		"slides": []map[string]any{{"layout": "svg", "title": "图", "svg": svgHead +
			`<image href="assets/map.png" x="100" y="100" width="640" height="640"><title>全省站点</title></image></svg>`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := checkPackage(t, mustRead(t, filepath.Join(root, "decks", "img.pptx")))
	s1 := parts["ppt/slides/slide1.xml"]
	// 640x360 picture fitted into a 640x640 box keeps its aspect ratio and is centred.
	if !strings.Contains(s1, `<p:pic>`) || !strings.Contains(s1, `descr="全省站点"`) || !strings.Contains(parts["ppt/slides/_rels/slide1.xml.rels"], "../media/image1.png") {
		t.Errorf("picture missing: %.400s", s1)
	}
	if _, err := testDeck().convertSVG(svgHead + `<image href="../outside.png" width="10" height="10"/></svg>`); err == nil {
		t.Error("an image outside the workspace was accepted")
	}
	if _, err := testDeck().convertSVG(svgHead + `<image href="http://example.com/a.png" width="10" height="10"/></svg>`); err == nil {
		t.Error("a remote image was accepted")
	}
}
