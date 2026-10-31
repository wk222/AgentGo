package officedoc

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxPptxBytes    = 200 << 20
	maxPartBytes    = 40 << 20
	defaultInspect  = 40
	maxSlideText    = 4000
	maxIssues       = 120
	tableSampleRows = 6
)

type inspectPptxInput struct {
	RelativePath string `json:"relative_path" jsonschema:"required,description=Workspace-relative path of an existing .pptx file\\, or builtin:<name> for a template compiled into AgentGo."`
	Slides       string `json:"slides,omitempty" jsonschema:"description=Which slides to describe\\, e.g. 1-3\\,7 (default: the first 40). Layout checks always cover the whole deck."`
	Layouts      bool   `json:"layouts,omitempty" jsonschema:"description=Also list the file's slide layouts (number\\, name\\, placeholders\\, logo/art positions\\, content area in svg px) and brand colour. Use it on a company template before create_pptx with template."`
}

type pptxTableInfo struct {
	Rows int        `json:"rows"`
	Cols int        `json:"cols"`
	Head []string   `json:"header,omitempty"`
	Data [][]string `json:"first_rows,omitempty"`
}

type pptxChartSeries struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type pptxChartInfo struct {
	Type       string            `json:"type"`
	Categories []string          `json:"categories,omitempty"`
	Series     []pptxChartSeries `json:"series"`
}

type pptxSlideInfo struct {
	N        int             `json:"n"`
	Layout   string          `json:"layout,omitempty"`
	Title    string          `json:"title"`
	Text     []string        `json:"text,omitempty"`
	Tables   []pptxTableInfo `json:"tables,omitempty"`
	Charts   []pptxChartInfo `json:"charts,omitempty"`
	Pictures int             `json:"pictures,omitempty"`
	Notes    string          `json:"notes,omitempty"`
}

type pptxIssue struct {
	Slide   int    `json:"slide"`
	Level   string `json:"level"` // warn or info
	Message string `json:"message"`
}

type inspectPptxOutput struct {
	Path   string          `json:"path"`
	Slides int             `json:"slides"`
	Size   string          `json:"slide_size"`
	Items  []pptxSlideInfo `json:"items"`
	Issues []pptxIssue     `json:"issues"`
	Notes  []string        `json:"notes,omitempty"`
	// Template is set when layouts was asked for.
	Template *tplInfo `json:"template,omitempty"`
}

// --- XML shapes (namespace-agnostic: encoding/xml matches local names) ---

type xXfrm struct {
	Off struct {
		X int64 `xml:"x,attr"`
		Y int64 `xml:"y,attr"`
	} `xml:"off"`
	Ext struct {
		Cx int64 `xml:"cx,attr"`
		Cy int64 `xml:"cy,attr"`
	} `xml:"ext"`
}

type xParaItem struct {
	XMLName xml.Name
	Lvl     int    `xml:"lvl,attr"`
	MarL    int64  `xml:"marL,attr"`
	T       string `xml:"t"`
	RPr     *struct {
		Sz int `xml:"sz,attr"`
	} `xml:"rPr"`
	BuChar *struct{} `xml:"buChar"`
	SpcBef *struct {
		Pts struct {
			Val int `xml:"val,attr"`
		} `xml:"spcPts"`
	} `xml:"spcBef"`
	LnSpc *struct {
		Pct struct {
			Val int `xml:"val,attr"`
		} `xml:"spcPct"`
	} `xml:"lnSpc"`
}

type xPara struct {
	Items []xParaItem `xml:",any"`
}

type xBody struct {
	BodyPr struct {
		LIns *int64    `xml:"lIns,attr"`
		TIns *int64    `xml:"tIns,attr"`
		RIns *int64    `xml:"rIns,attr"`
		BIns *int64    `xml:"bIns,attr"`
		Norm *struct{} `xml:"normAutofit"`
		Sp   *struct{} `xml:"spAutoFit"`
	} `xml:"bodyPr"`
	Paras []xPara `xml:"p"`
}

type xNV struct {
	C struct {
		Name  string `xml:"name,attr"`
		Descr string `xml:"descr,attr"`
	} `xml:"cNvPr"`
	Ph *struct {
		Type string `xml:"type,attr"`
	} `xml:"nvPr>ph"`
}

type xShape struct {
	NV   xNV `xml:"nvSpPr"`
	SpPr struct {
		Xfrm *xXfrm `xml:"xfrm"`
	} `xml:"spPr"`
	Body *xBody `xml:"txBody"`
}

type xPic struct {
	NV   xNV `xml:"nvPicPr"`
	SpPr struct {
		Xfrm *xXfrm `xml:"xfrm"`
	} `xml:"spPr"`
}

type xFrame struct {
	NV    xNV    `xml:"nvGraphicFramePr"`
	Xfrm  *xXfrm `xml:"xfrm"`
	Chart *struct {
		ID string `xml:"id,attr"`
	} `xml:"graphic>graphicData>chart"`
	Table *struct {
		Rows []struct {
			Cells []struct {
				Body xBody `xml:"txBody"`
			} `xml:"tc"`
		} `xml:"tr"`
	} `xml:"graphic>graphicData>tbl"`
}

type xTree struct {
	Shapes []xShape `xml:"sp"`
	Pics   []xPic   `xml:"pic"`
	Frames []xFrame `xml:"graphicFrame"`
	Groups []xTree  `xml:"grpSp"`
}

type xSlide struct {
	Tree xTree `xml:"cSld>spTree"`
}

func (t xTree) flatten() (sh []xShape, pics []xPic, frames []xFrame) {
	sh, pics, frames = append(sh, t.Shapes...), append(pics, t.Pics...), append(frames, t.Frames...)
	for _, g := range t.Groups {
		a, b, c := g.flatten()
		sh, pics, frames = append(sh, a...), append(pics, b...), append(frames, c...)
	}
	return
}

type pptxPackage struct {
	parts map[string][]byte
}

func openPptx(env Env, relPath string) (*pptxPackage, string, error) {
	rel := strings.TrimSpace(relPath)
	if rel == "" {
		return nil, "", errors.New("relative_path is required")
	}
	if isBuiltinRef(rel) {
		zr, name, err := openBuiltin(rel)
		if err != nil {
			return nil, "", err
		}
		pkg, err := pptxFromZip(zr.File, "built-in template "+name)
		return pkg, builtinPrefix + name, err
	}
	if ext := strings.ToLower(filepath.Ext(rel)); ext != ".pptx" && ext != ".potx" {
		return nil, "", errors.New("only .pptx and .potx files are supported (save .ppt as .pptx first)")
	}
	abs, err := env.Resolve(rel)
	if err != nil {
		return nil, "", err
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, "", fmt.Errorf("cannot open %s as a presentation: %w", filepath.Base(abs), err)
	}
	defer zr.Close()
	pkg, err := pptxFromZip(zr.File, filepath.Base(abs))
	return pkg, rel, err
}

// pptxFromZip reads the XML parts of a package; label names the file in messages.
func pptxFromZip(files []*zip.File, label string) (*pptxPackage, error) {
	var total uint64
	pkg := &pptxPackage{parts: map[string][]byte{}}
	for _, f := range files {
		name := f.Name
		isXML := strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels")
		if !isXML {
			continue
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > maxPartBytes || total > maxPptxBytes {
			return nil, fmt.Errorf("%s is too large to inspect", label)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		pkg.parts[name] = b
	}
	if _, ok := pkg.parts["ppt/presentation.xml"]; !ok {
		return nil, errors.New("not a PowerPoint file: ppt/presentation.xml is missing")
	}
	return pkg, nil
}

type relEntry struct{ ID, Type, Target string }

// rels reads the relationships of a part (the .rels file next to it).
func (p *pptxPackage) rels(part string) []relEntry {
	data := p.parts[path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")]
	var doc struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := make([]relEntry, len(doc.Rel))
	for i, r := range doc.Rel {
		target := r.Target
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(path.Dir(part), target)
		}
		out[i] = relEntry{ID: r.ID, Type: r.Type, Target: target}
	}
	return out
}

func (p *pptxPackage) slideOrder() []string {
	var pres struct {
		Ids []struct {
			RID string `xml:"id,attr"`
		} `xml:"sldIdLst>sldId"`
	}
	_ = xml.Unmarshal(p.parts["ppt/presentation.xml"], &pres)
	byID := map[string]string{}
	for _, r := range p.rels("ppt/presentation.xml") {
		byID[r.ID] = r.Target
	}
	var out []string
	for _, id := range pres.Ids {
		if t := byID[id.RID]; t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (p *pptxPackage) slideSize() (w, h float64) {
	var pres struct {
		Sz struct {
			Cx int64 `xml:"cx,attr"`
			Cy int64 `xml:"cy,attr"`
		} `xml:"sldSz"`
	}
	_ = xml.Unmarshal(p.parts["ppt/presentation.xml"], &pres)
	if pres.Sz.Cx <= 0 || pres.Sz.Cy <= 0 {
		return slideW, slideH
	}
	return float64(pres.Sz.Cx) / emuPerInch, float64(pres.Sz.Cy) / emuPerInch
}

func parseSlideSelection(s string, total int) (map[int]bool, error) {
	sel := map[int]bool{}
	s = strings.TrimSpace(s)
	if s == "" {
		for i := 1; i <= min(total, defaultInspect); i++ {
			sel[i] = true
		}
		return sel, nil
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ' ' }) {
		a, b, isRange := strings.Cut(part, "-")
		lo, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("slides %q: %q is not a slide number or range", s, part)
		}
		hi := lo
		if isRange {
			if hi, err = strconv.Atoi(strings.TrimSpace(b)); err != nil {
				return nil, fmt.Errorf("slides %q: %q is not a slide number or range", s, part)
			}
		}
		if lo < 1 || hi < lo || hi > total {
			return nil, fmt.Errorf("slides %q: out of range (the deck has %d slides)", s, total)
		}
		for i := lo; i <= hi; i++ {
			sel[i] = true
		}
	}
	return sel, nil
}

func inspectPptx(env Env, in inspectPptxInput) (inspectPptxOutput, error) {
	pkg, rel, err := openPptx(env, in.RelativePath)
	if err != nil {
		return inspectPptxOutput{}, err
	}
	builtin := isBuiltinRef(in.RelativePath)
	if builtin {
		in.Layouts = true // a built-in template is only worth reading for its layouts
	}
	order := pkg.slideOrder()
	if len(order) == 0 && !in.Layouts {
		return inspectPptxOutput{}, errors.New("the presentation has no slides")
	}
	sel, err := parseSlideSelection(in.Slides, len(order))
	if err != nil && len(order) > 0 {
		return inspectPptxOutput{}, err
	}
	w, h := pkg.slideSize()
	// Issues stays a non-nil slice: "issues":[] reads as "checked, nothing found" where null is ambiguous.
	out := inspectPptxOutput{Path: filepath.ToSlash(rel), Slides: len(order), Size: fmt.Sprintf("%.2f x %.2f in", w, h), Issues: []pptxIssue{}}
	if in.Layouts {
		t := &template{}
		if err := t.analyze(pkg); err != nil {
			return inspectPptxOutput{}, err
		}
		info := t.describe()
		out.Template = &info
	}
	if strings.TrimSpace(in.Slides) == "" && len(order) > defaultInspect {
		out.Notes = append(out.Notes, fmt.Sprintf("only the first %d slides are described; pass slides to see others", defaultInspect))
	}
	if builtin {
		out.Notes = append(out.Notes, "built-in template: its slides are empty stand-ins that only record which layouts the original used; pass the name to create_pptx as template")
		order = nil
	}
	for i, part := range order {
		n := i + 1
		var doc xSlide
		if err := xml.Unmarshal(pkg.parts[part], &doc); err != nil {
			out.Issues = append(out.Issues, pptxIssue{Slide: n, Level: "warn", Message: "the slide XML cannot be read: " + err.Error()})
			continue
		}
		info, issues := pkg.describeSlide(n, part, doc, w, h)
		out.Issues = append(out.Issues, issues...)
		if sel[n] {
			out.Items = append(out.Items, info)
		}
	}
	sortIssues(out.Issues)
	if len(out.Issues) > maxIssues {
		out.Notes = append(out.Notes, fmt.Sprintf("%d more issues not listed", len(out.Issues)-maxIssues))
		out.Issues = out.Issues[:maxIssues]
	}
	return out, nil
}

func isTitlePH(sh xShape) bool {
	return sh.NV.Ph != nil && (sh.NV.Ph.Type == "title" || sh.NV.Ph.Type == "ctrTitle")
}

func isFooterShape(sh xShape) bool {
	if sh.NV.Ph != nil {
		switch sh.NV.Ph.Type {
		case "ftr", "sldNum", "dt":
			return true
		}
	}
	name := strings.ToLower(sh.NV.C.Name)
	return strings.Contains(name, "footer") || strings.Contains(name, "slide number")
}

// paraText returns a paragraph's text, its font size in pt (0 = inherited) and
// whether it is a bullet. Runs of different size report the largest.
func paraText(p xPara) (text string, size float64, bullet bool, level int, marL int64, before float64) {
	text, size, bullet, level, marL, before, _ = paraFull(p)
	return
}

// paraFull is paraText plus the line spacing multiple (1 = single).
func paraFull(p xPara) (text string, size float64, bullet bool, level int, marL int64, before, spacing float64) {
	var b strings.Builder
	spacing = 1
	for _, it := range p.Items {
		switch it.XMLName.Local {
		case "pPr":
			level, marL = it.Lvl, it.MarL
			bullet = it.BuChar != nil
			if it.SpcBef != nil {
				before = float64(it.SpcBef.Pts.Val) / 100
			}
			if it.LnSpc != nil && it.LnSpc.Pct.Val > 0 {
				spacing = float64(it.LnSpc.Pct.Val) / 100000
			}
		case "r", "fld":
			b.WriteString(it.T)
			if it.RPr != nil && it.RPr.Sz > 0 {
				size = max(size, float64(it.RPr.Sz)/100)
			}
		case "br":
			b.WriteString("\n")
		}
	}
	return b.String(), size, bullet, level, marL, before, spacing
}

func (b *xBody) fullText() string {
	var lines []string
	for _, p := range b.Paras {
		t, _, bullet, level, _, _ := paraText(p)
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// Level 0 in a body placeholder may be bulleted by the layout; only
		// explicit bullets and indented levels are marked.
		if bullet || level > 0 {
			t = strings.Repeat("  ", level) + "- " + t
		}
		lines = append(lines, t)
	}
	return strings.Join(lines, "\n")
}

type box struct {
	name       string
	x, y, w, h float64 // inches
	text       bool
}

func inches(v int64) float64 { return float64(v) / emuPerInch }

func (x *xXfrm) rect() (px, py, pw, ph float64, ok bool) {
	if x == nil || (x.Ext.Cx == 0 && x.Ext.Cy == 0) {
		return 0, 0, 0, 0, false
	}
	return inches(x.Off.X), inches(x.Off.Y), inches(x.Ext.Cx), inches(x.Ext.Cy), true
}

func (p *pptxPackage) describeSlide(n int, part string, doc xSlide, slideWIn, slideHIn float64) (pptxSlideInfo, []pptxIssue) {
	info := pptxSlideInfo{N: n}
	var issues []pptxIssue
	warn := func(level, format string, args ...any) {
		issues = append(issues, pptxIssue{Slide: n, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	rels := p.rels(part)
	for _, r := range rels {
		switch {
		case strings.HasSuffix(r.Type, "/slideLayout"):
			var lay struct {
				Name string `xml:"name,attr"`
			}
			_ = xmlAttrOf(p.parts[r.Target], "cSld", &lay)
			info.Layout = lay.Name
		case strings.HasSuffix(r.Type, "/notesSlide"):
			var ns xSlide
			if xml.Unmarshal(p.parts[r.Target], &ns) == nil {
				shapes, _, _ := ns.Tree.flatten()
				var lines []string
				for _, sh := range shapes {
					if sh.NV.Ph != nil && sh.NV.Ph.Type == "body" && sh.Body != nil {
						lines = append(lines, sh.Body.fullText())
					}
				}
				info.Notes = strings.TrimSpace(strings.Join(lines, "\n"))
			}
		}
	}

	shapes, pics, frames := doc.Tree.flatten()
	var boxes []box
	var textChars int
	minSize := 0.0
	for _, sh := range shapes {
		var text string
		if sh.Body != nil {
			text = sh.Body.fullText()
		}
		x, y, w, h, hasGeom := sh.SpPr.Xfrm.rect()
		if isTitlePH(sh) {
			info.Title = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
		} else if strings.TrimSpace(text) != "" && !isFooterShape(sh) {
			info.Text = append(info.Text, clipText(text, maxSlideText))
			textChars += len([]rune(text))
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		footer := isFooterShape(sh)
		if sh.Body != nil && hasGeom && !footer {
			need, avail := textNeed(sh.Body, w, h)
			switch {
			case sh.Body.BodyPr.Sp != nil:
				h = max(h, need) // "resize shape to fit text": the box grows with its text
			case need > avail*1.08+0.02 && !(sh.Body.BodyPr.Norm != nil && need < avail*1.5):
				// normAutofit shrinks the text, so only a large excess is a problem.
				warn("warn", "text in %q may overflow its box: needs about %.1f in, the box is %.1f in tall", sh.NV.C.Name, need, avail)
			}
			if sz := smallestSize(sh.Body); sz > 0 && (minSize == 0 || sz < minSize) {
				minSize = sz
			}
		}
		if hasGeom {
			boxes = append(boxes, box{name: sh.NV.C.Name, x: x, y: y, w: w, h: h, text: true})
		}
	}
	if minSize > 0 && minSize < 10 {
		warn("warn", "very small text (%.0f pt); projected slides need 12 pt or more", minSize)
	}
	info.Pictures = len(pics)
	for _, pic := range pics {
		if x, y, w, h, ok := pic.SpPr.Xfrm.rect(); ok {
			boxes = append(boxes, box{name: pic.NV.C.Name, x: x, y: y, w: w, h: h})
		}
		if strings.TrimSpace(pic.NV.C.Descr) == "" {
			warn("info", "picture %q has no alt text", pic.NV.C.Name)
		}
	}
	for _, fr := range frames {
		if x, y, w, h, ok := fr.Xfrm.rect(); ok {
			boxes = append(boxes, box{name: fr.NV.C.Name, x: x, y: y, w: w, h: h})
		}
		switch {
		case fr.Table != nil:
			t := pptxTableInfo{Rows: len(fr.Table.Rows)}
			for ri, r := range fr.Table.Rows {
				t.Cols = max(t.Cols, len(r.Cells))
				if ri >= tableSampleRows {
					continue
				}
				row := make([]string, len(r.Cells))
				for ci, c := range r.Cells {
					row[ci] = strings.ReplaceAll(c.Body.fullText(), "\n", " ")
				}
				if ri == 0 {
					t.Head = row
				} else {
					t.Data = append(t.Data, row)
				}
			}
			info.Tables = append(info.Tables, t)
		case fr.Chart != nil:
			for _, r := range rels {
				if r.ID == fr.Chart.ID {
					if c, ok := describeChart(p.parts[r.Target]); ok {
						info.Charts = append(info.Charts, c)
					}
				}
			}
			if strings.TrimSpace(fr.NV.C.Descr) == "" {
				warn("info", "chart %q has no alt text", fr.NV.C.Name)
			}
		}
	}

	if info.Title == "" {
		warn("warn", "the slide has no title (add a title placeholder so the outline and screen readers work)")
	}
	if len(shapes) == 0 && len(pics) == 0 && len(frames) == 0 {
		warn("warn", "the slide is empty")
	}
	if textChars > 600 {
		warn("info", "dense slide: about %d characters of text; consider splitting it", textChars)
	}
	for _, b := range boxes {
		if b.x < -0.05 || b.y < -0.05 || b.x+b.w > slideWIn+0.05 || b.y+b.h > slideHIn+0.05 {
			warn("warn", "%q extends past the slide edge", b.name)
		}
	}
	issues = append(issues, overlaps(n, boxes)...)
	return info, issues
}

// overlaps reports pairs of content shapes that cover each other substantially.
func overlaps(n int, boxes []box) []pptxIssue {
	var out []pptxIssue
	for i := 0; i < len(boxes); i++ {
		for j := i + 1; j < len(boxes); j++ {
			a, b := boxes[i], boxes[j]
			ix := min(a.x+a.w, b.x+b.w) - max(a.x, b.x)
			iy := min(a.y+a.h, b.y+b.h) - max(a.y, b.y)
			if ix <= 0 || iy <= 0 {
				continue
			}
			if inter := ix * iy; inter > 0.2*min(a.w*a.h, b.w*b.h) {
				out = append(out, pptxIssue{Slide: n, Level: "warn", Message: fmt.Sprintf("%q and %q overlap", a.name, b.name)})
			}
		}
	}
	return out
}

// textNeed estimates the height a text body needs and the height it has.
func textNeed(b *xBody, wIn, hIn float64) (need, avail float64) {
	ins := func(v *int64, def int64) float64 {
		if v != nil {
			return inches(*v)
		}
		return inches(def)
	}
	l, r := ins(b.BodyPr.LIns, 91440), ins(b.BodyPr.RIns, 91440)
	t, bt := ins(b.BodyPr.TIns, 45720), ins(b.BodyPr.BIns, 45720)
	var pts float64
	for _, p := range b.Paras {
		text, size, bullet, _, marL, before, spacing := paraFull(p)
		if size == 0 {
			size = 18
		}
		width := (wIn-l-r)*ptPerInch - float64(marL)/emuPerInch*ptPerInch
		if bullet && marL == 0 {
			width -= 0.3 * ptPerInch
		}
		pts += float64(countLines(text, max(width, size)/size))*size*lineFactor*spacing + before
	}
	return pts/ptPerInch + t + bt, hIn
}

func smallestSize(b *xBody) float64 {
	low := 0.0
	for _, p := range b.Paras {
		if t, size, _, _, _, _ := paraText(p); strings.TrimSpace(t) != "" && size > 0 && (low == 0 || size < low) {
			low = size
		}
	}
	return low
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("…(+%d chars)", len(r)-n)
}

// xmlAttrOf decodes the attributes of the first element named local into dst.
func xmlAttrOf(data []byte, local string, dst any) error {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == local {
			return dec.DecodeElement(dst, &se)
		}
	}
}

type xPlot struct {
	XMLName xml.Name
	BarDir  struct {
		Val string `xml:"val,attr"`
	} `xml:"barDir"`
	Grouping struct {
		Val string `xml:"val,attr"`
	} `xml:"grouping"`
	Sers []struct {
		Name    []string `xml:"tx>strRef>strCache>pt>v"`
		Cats    []string `xml:"cat>strRef>strCache>pt>v"`
		CatsNum []string `xml:"cat>numRef>numCache>pt>v"`
		Vals    []string `xml:"val>numRef>numCache>pt>v"`
	} `xml:"ser"`
}

func describeChart(data []byte) (pptxChartInfo, bool) {
	var cs struct {
		Area *struct {
			Plots []xPlot `xml:",any"`
		} `xml:"chart>plotArea"`
	}
	if xml.Unmarshal(data, &cs) != nil || cs.Area == nil {
		return pptxChartInfo{}, false
	}
	var out pptxChartInfo
	for _, pl := range cs.Area.Plots {
		if !strings.HasSuffix(pl.XMLName.Local, "Chart") {
			continue
		}
		kind := strings.TrimSuffix(pl.XMLName.Local, "Chart")
		switch {
		case kind == "bar" && pl.BarDir.Val == "bar":
			kind = "bar (horizontal)"
		case kind == "bar":
			kind = "column"
		}
		if pl.Grouping.Val == "stacked" || pl.Grouping.Val == "percentStacked" {
			kind += " (" + pl.Grouping.Val + ")"
		}
		if out.Type == "" {
			out.Type = kind
		} else {
			out.Type += " + " + kind
		}
		for _, s := range pl.Sers {
			if len(out.Categories) == 0 {
				out.Categories = append(s.Cats, s.CatsNum...)
			}
			name := ""
			if len(s.Name) > 0 {
				name = s.Name[0]
			}
			out.Series = append(out.Series, pptxChartSeries{Name: name, Values: s.Vals})
		}
	}
	return out, out.Type != ""
}

// sortIssues orders issues by slide, warnings first.
func sortIssues(is []pptxIssue) {
	sort.SliceStable(is, func(i, j int) bool {
		if is[i].Slide != is[j].Slide {
			return is[i].Slide < is[j].Slide
		}
		return is[i].Level == "warn" && is[j].Level != "warn"
	})
}
