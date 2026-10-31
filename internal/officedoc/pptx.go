package officedoc

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxSlides   = 60
	defaultFont = "Microsoft YaHei"
)

type createPptxInput struct {
	RelativePath  string        `json:"relative_path" jsonschema:"required,description=Workspace-relative path of the .pptx to create (e.g. decks/q3-review.pptx). The .pptx extension is added when missing."`
	Title         string        `json:"title" jsonschema:"required,description=Deck title: shown on the cover slide and in the file properties."`
	Subtitle      string        `json:"subtitle,omitempty" jsonschema:"description=Cover subtitle (for example the period or the audience)."`
	Author        string        `json:"author,omitempty" jsonschema:"description=Presenter or department: cover line and file properties."`
	Date          string        `json:"date,omitempty" jsonschema:"description=Date text for the cover\\, e.g. 2026年10月."`
	Theme         string        `json:"theme,omitempty" jsonschema:"description=Colour theme: blue (default)\\, mobile\\, green\\, red or gray."`
	Font          string        `json:"font,omitempty" jsonschema:"description=Font family (default Microsoft YaHei)."`
	NoCover       bool          `json:"no_cover,omitempty" jsonschema:"description=Do not generate the cover slide."`
	Closing       string        `json:"closing,omitempty" jsonschema:"description=Text of a closing slide\\, e.g. 谢谢. Empty: no closing slide."`
	Footer        string        `json:"footer,omitempty" jsonschema:"description=Small footer on content slides (default: the deck title\\; - for none)."`
	Slides        []pptxSlideIn `json:"slides" jsonschema:"required,description=The slides after the cover\\, in order."`
	Template      string        `json:"template,omitempty" jsonschema:"description=Workspace-relative path of a .pptx/.potx company template to build on\\, or builtin:<name> for a template compiled into AgentGo (inspect_pptx builtin:<name> lists its layouts): its masters\\, layouts\\, logo and background art are kept and its own slides are not copied. Cover\\, title and page number come from its layouts; footer is ignored and the colours of generated shapes follow the template's brand colour unless theme is given. Learn it first with inspect_pptx layouts=true."`
	CoverLayout   string        `json:"cover_layout,omitempty" jsonschema:"description=Template mode: layout (number or name) for the cover. Default: what the template's first slide uses."`
	ContentLayout string        `json:"content_layout,omitempty" jsonschema:"description=Template mode: layout for content slides. Default: the layout most of the template's slides use."`
	ClosingLayout string        `json:"closing_layout,omitempty" jsonschema:"description=Template mode: layout for the closing slide. Default: the cover layout."`
	Overwrite     bool          `json:"overwrite,omitempty" jsonschema:"description=Replace the file if it exists. Default false: an existing file is an error."`
}

type pptxSlideIn struct {
	Layout   string        `json:"layout" jsonschema:"required,description=bullets | two_column | table | chart | stats | timeline | image | section | svg. Which of the other fields apply depends on it."`
	Title    string        `json:"title" jsonschema:"required,description=Slide title: say the conclusion\\, not the topic (one line)."`
	Body     string        `json:"body,omitempty" jsonschema:"description=section: a one-line subtitle."`
	Bullets  []string      `json:"bullets,omitempty" jsonschema:"description=bullets: the points\\, at most about 7\\, one idea each. A point starting with \"- \" is a sub-point. **text** is bold."`
	Left     *pptxColumnIn `json:"left,omitempty" jsonschema:"description=two_column: left column."`
	Right    *pptxColumnIn `json:"right,omitempty" jsonschema:"description=two_column: right column."`
	Columns  []string      `json:"columns,omitempty" jsonschema:"description=table: header cells (1-8)."`
	Rows     [][]any       `json:"rows,omitempty" jsonschema:"description=table: rows of cells\\, each as long as columns. A long table is split over several slides automatically."`
	Chart    *pptxChartIn  `json:"chart,omitempty" jsonschema:"description=chart: the chart (native and editable in PowerPoint/WPS)."`
	Takeaway []string      `json:"takeaway,omitempty" jsonschema:"description=chart: key points shown beside the chart (optional\\, at most 4)."`
	Stats    []pptxStatIn  `json:"stats,omitempty" jsonschema:"description=stats: 2-6 key numbers."`
	Steps    []pptxStepIn  `json:"steps,omitempty" jsonschema:"description=timeline: 2-6 steps."`
	Image    string        `json:"image,omitempty" jsonschema:"description=image: workspace-relative path of a .png or .jpg."`
	Caption  string        `json:"caption,omitempty" jsonschema:"description=table or image: a small note under it\\, e.g. the data source."`
	Svg      string        `json:"svg,omitempty" jsonschema:"description=svg: the whole slide as ONE constrained SVG document\\, viewBox=\"0 0 1280 720\"\\, converted to native editable PowerPoint shapes. Allowed: rect circle ellipse line polyline polygon path text/tspan image g\\, linear/radialGradient\\, feDropShadow. No CSS\\, use\\, clipPath\\, mask\\, pattern. Give the main heading id=\"title\". Wrong SVG is rejected with a list of problems. Read the office-pptx skill first."`
	Notes    string        `json:"notes,omitempty" jsonschema:"description=Speaker notes for the slide."`

	TemplateLayout string `json:"template_layout,omitempty" jsonschema:"description=Template mode only: the template layout (number or name from inspect_pptx layouts) this slide sits on. Default: content_layout."`
}

type createPptxOutput struct {
	createOutput
	Slides   int      `json:"slides"`
	Outline  []string `json:"outline"`
	Warnings []string `json:"warnings,omitempty"`
}

// deck is the state of one presentation being built.
type deck struct {
	env      Env
	pal      palette
	font     string
	title    string
	footer   string
	numbers  bool
	slides   []*slide
	charts   []chartPart
	images   []imagePart
	warnings []string
	cur      int // number of the slide being built, for warnings

	// fr is the area content slides are laid out in: the built-in grid, or the
	// content area of the template layout the current slide sits on.
	fr area
	// Template mode (create_pptx with template): the package of the user's
	// file supplies masters, layouts and theme; tpl* are the layouts chosen.
	tpl                            *template
	tplCover, tplContent, tplClose *tplLayout
	curTpl                         *tplLayout // layout of the slide being built
}

// area is a rectangle in inches.
type area struct{ x, y, w, h float64 }

var defaultFrame = area{x: marginX, y: bodyY, w: contentW, h: bodyH}

func createPptx(env Env, in createPptxInput) (createPptxOutput, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return createPptxOutput{}, errors.New("title is required")
	}
	if len(in.Slides) == 0 {
		return createPptxOutput{}, errors.New("slides is required")
	}
	if len(in.Slides) > maxSlides {
		return createPptxOutput{}, fmt.Errorf("too many slides: %d (max %d)", len(in.Slides), maxSlides)
	}
	var tpl *template
	if strings.TrimSpace(in.Template) != "" {
		var err error
		if tpl, err = loadTemplate(env, in.Template); err != nil {
			return createPptxOutput{}, err
		}
	}
	themeName := strings.ToLower(strings.TrimSpace(in.Theme))
	pal, ok := palettes[themeName]
	switch {
	case themeName == "" && tpl != nil && tpl.brand != "":
		pal, ok = brandPalette(tpl.brand), true // generated shapes take the template's colour
	case themeName == "":
		pal, ok = palettes["blue"], true
	}
	if !ok {
		return createPptxOutput{}, fmt.Errorf("theme %q must be one of %s", in.Theme, themeNames)
	}
	font := strings.TrimSpace(in.Font)
	if font == "" {
		font = defaultFont
	}
	if len([]rune(font)) > 60 {
		return createPptxOutput{}, errors.New("font name is too long")
	}
	rel := withExt(in.RelativePath, ".pptx")
	abs, err := env.Resolve(rel)
	if err != nil {
		return createPptxOutput{}, err
	}

	d := &deck{env: env, pal: pal, font: font, title: title, numbers: true, fr: defaultFrame, tpl: tpl}
	switch f := strings.TrimSpace(in.Footer); f {
	case "":
		d.footer = title
	case "-":
	default:
		d.footer = f
	}
	if tpl != nil {
		if strings.EqualFold(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "./"), strings.TrimPrefix(filepath.ToSlash(filepath.Clean(tpl.rel)), "./")) {
			return createPptxOutput{}, errors.New("relative_path must not be the template itself")
		}
		var err error
		if d.tplContent, err = tpl.pick(in.ContentLayout, tpl.content); err != nil {
			return createPptxOutput{}, fmt.Errorf("content_layout: %w", err)
		}
		if d.tplCover, err = tpl.pick(in.CoverLayout, firstLayout(tpl.cover, tpl.content)); err != nil {
			return createPptxOutput{}, fmt.Errorf("cover_layout: %w", err)
		}
		if d.tplClose, err = tpl.pick(in.ClosingLayout, d.tplCover); err != nil {
			return createPptxOutput{}, fmt.Errorf("closing_layout: %w", err)
		}
	}

	if !in.NoCover {
		d.cur = 1
		if tpl != nil {
			d.slides = append(d.slides, d.tplHero(d.tplCover, title, nonEmpty(in.Subtitle, in.Author, in.Date), ""))
		} else {
			meta := strings.TrimSpace(strings.Join(nonEmpty(in.Author, in.Date), "  |  "))
			d.slides = append(d.slides, d.heroSlide(title, in.Subtitle, meta, "", true))
		}
	}
	for i, sl := range in.Slides {
		d.cur = len(d.slides) + 1
		built, err := d.buildSlide(sl)
		if err != nil {
			return createPptxOutput{}, fmt.Errorf("slides[%d] (%s): %w", i, strings.TrimSpace(sl.Layout), err)
		}
		d.slides = append(d.slides, built...)
	}
	if c := strings.TrimSpace(in.Closing); c != "" {
		d.cur = len(d.slides) + 1
		if tpl != nil {
			d.slides = append(d.slides, d.tplHero(d.tplClose, c, nil, ""))
		} else {
			d.slides = append(d.slides, d.heroSlide(c, "", strings.TrimSpace(strings.Join(nonEmpty(in.Author, in.Date), "  |  ")), "", false))
		}
	}

	var data []byte
	if tpl != nil {
		data, err = d.packTemplate(title, strings.TrimSpace(in.Author))
	} else {
		data, err = d.pack(title, strings.TrimSpace(in.Author))
	}
	if err != nil {
		return createPptxOutput{}, err
	}
	if err := writeNew(abs, data, in.Overwrite); err != nil {
		return createPptxOutput{}, err
	}
	out := createPptxOutput{
		createOutput: createOutput{Path: filepath.ToSlash(rel), Bytes: len(data), Detail: fmt.Sprintf("%d slides, %d charts", len(d.slides), len(d.charts))},
		Slides:       len(d.slides),
		Warnings:     d.warnings,
	}
	for i, s := range d.slides {
		out.Outline = append(out.Outline, fmt.Sprintf("%d. %s", i+1, firstNonEmpty(s.title, "(untitled)")))
	}
	return out, nil
}

func nonEmpty(items ...string) []string {
	var out []string
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (d *deck) buildSlide(in pptxSlideIn) ([]*slide, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, errors.New("title is required")
	}
	in.Title = strings.TrimSpace(in.Title)
	one := func(s *slide, err error) ([]*slide, error) {
		if err != nil {
			return nil, err
		}
		return []*slide{s}, nil
	}
	kind := strings.ToLower(strings.TrimSpace(in.Layout))
	if d.tpl != nil {
		switch kind {
		case "bullets", "two_column", "table", "chart", "stats", "timeline", "image", "section", "svg":
			if err := d.useTemplateLayout(in, kind); err != nil {
				return nil, err
			}
		}
	}
	switch kind {
	case "bullets":
		return one(d.bulletsSlide(in))
	case "two_column":
		return one(d.twoColumnSlide(in))
	case "table":
		return d.tableSlides(in)
	case "chart":
		return one(d.chartSlide(in))
	case "stats":
		return one(d.statsSlide(in))
	case "timeline":
		return one(d.timelineSlide(in))
	case "image":
		return one(d.imageSlide(in))
	case "section":
		return one(d.sectionSlide(in))
	case "svg":
		return one(d.svgSlide(in))
	}
	return nil, fmt.Errorf("layout %q must be one of bullets, two_column, table, chart, stats, timeline, image, section, svg", in.Layout)
}

const (
	ctPresentation = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	pmlPrefix      = "application/vnd.openxmlformats-officedocument.presentationml."
	relBase        = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	relsHead       = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
)

type pkgFile struct {
	name string
	body []byte
}

func relXML(id, typ, target string) string {
	return `<Relationship Id="` + id + `" Type="` + typ + `" Target="` + target + `"/>`
}

// pack assembles the package.
func (d *deck) pack(title, author string) ([]byte, error) {
	now := time.Now().UTC()
	hasNotes := false
	for _, s := range d.slides {
		if strings.TrimSpace(s.notes) != "" {
			hasNotes = true
		}
	}

	var files []pkgFile
	add := func(name, body string) { files = append(files, pkgFile{name, []byte(body)}) }

	// Content types.
	var ct strings.Builder
	ct.WriteString(xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>`)
	if len(d.charts) > 0 {
		ct.WriteString(`<Default Extension="xlsx" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"/>`)
	}
	hasPNG, hasJPG := false, false
	for _, im := range d.images {
		hasPNG = hasPNG || strings.HasSuffix(im.name, ".png")
		hasJPG = hasJPG || strings.HasSuffix(im.name, ".jpg")
	}
	if hasPNG {
		ct.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	}
	if hasJPG {
		ct.WriteString(`<Default Extension="jpg" ContentType="image/jpeg"/>`)
	}
	over := func(part, typ string) {
		ct.WriteString(`<Override PartName="` + part + `" ContentType="` + typ + `"/>`)
	}
	over("/ppt/presentation.xml", ctPresentation)
	over("/ppt/slideMasters/slideMaster1.xml", pmlPrefix+"slideMaster+xml")
	for i := 1; i <= layoutCount; i++ {
		over(fmt.Sprintf("/ppt/slideLayouts/slideLayout%d.xml", i), pmlPrefix+"slideLayout+xml")
	}
	over("/ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml")
	over("/ppt/presProps.xml", pmlPrefix+"presProps+xml")
	over("/ppt/tableStyles.xml", pmlPrefix+"tableStyles+xml")
	for i := range d.slides {
		over(fmt.Sprintf("/ppt/slides/slide%d.xml", i+1), pmlPrefix+"slide+xml")
		if strings.TrimSpace(d.slides[i].notes) != "" {
			over(fmt.Sprintf("/ppt/notesSlides/notesSlide%d.xml", i+1), pmlPrefix+"notesSlide+xml")
		}
	}
	if hasNotes {
		over("/ppt/notesMasters/notesMaster1.xml", pmlPrefix+"notesMaster+xml")
		over("/ppt/theme/theme2.xml", "application/vnd.openxmlformats-officedocument.theme+xml")
	}
	for i := range d.charts {
		over(fmt.Sprintf("/ppt/charts/chart%d.xml", i+1), "application/vnd.openxmlformats-officedocument.drawingml.chart+xml")
	}
	over("/docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml")
	over("/docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml")
	ct.WriteString(`</Types>`)
	add("[Content_Types].xml", ct.String())

	add("_rels/.rels", relsHead+
		relXML("rId1", relBase+"officeDocument", "ppt/presentation.xml")+
		relXML("rId2", "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties", "docProps/core.xml")+
		relXML("rId3", relBase+"extended-properties", "docProps/app.xml")+`</Relationships>`)
	add("docProps/core.xml", corePropsXML(title, author, now))
	notesCount := 0
	for _, s := range d.slides {
		if strings.TrimSpace(s.notes) != "" {
			notesCount++
		}
	}
	add("docProps/app.xml", xmlHeader+`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">`+
		`<Application>AgentGo</Application><Slides>`+fmt.Sprint(len(d.slides))+`</Slides><Notes>`+fmt.Sprint(notesCount)+`</Notes></Properties>`)

	// Presentation part.
	var pres, presRels strings.Builder
	presRels.WriteString(relsHead + relXML("rId1", relBase+"slideMaster", "slideMasters/slideMaster1.xml"))
	next := 2
	notesRID := ""
	if hasNotes {
		notesRID = fmt.Sprintf("rId%d", next)
		presRels.WriteString(relXML(notesRID, relBase+"notesMaster", "notesMasters/notesMaster1.xml"))
		next++
	}
	pres.WriteString(xmlHeader + `<p:presentation ` + nsA + ` ` + nsR + ` ` + nsP + ` saveSubsetFonts="1"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	if hasNotes {
		pres.WriteString(`<p:notesMasterIdLst><p:notesMasterId r:id="` + notesRID + `"/></p:notesMasterIdLst>`)
	}
	pres.WriteString(`<p:sldIdLst>`)
	for i := range d.slides {
		rid := fmt.Sprintf("rId%d", next)
		fmt.Fprintf(&pres, `<p:sldId id="%d" r:id="%s"/>`, 256+i, rid)
		presRels.WriteString(relXML(rid, relBase+"slide", fmt.Sprintf("slides/slide%d.xml", i+1)))
		next++
	}
	pres.WriteString(`</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`)
	for _, r := range []struct{ typ, target string }{
		{"presProps", "presProps.xml"}, {"tableStyles", "tableStyles.xml"}, {"theme", "theme/theme1.xml"},
	} {
		presRels.WriteString(relXML(fmt.Sprintf("rId%d", next), relBase+r.typ, r.target))
		next++
	}
	presRels.WriteString(`</Relationships>`)
	add("ppt/presentation.xml", pres.String())
	add("ppt/_rels/presentation.xml.rels", presRels.String())
	add("ppt/presProps.xml", xmlHeader+`<p:presentationPr `+nsA+` `+nsR+` `+nsP+`/>`)
	add("ppt/tableStyles.xml", xmlHeader+`<a:tblStyleLst `+nsA+` def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)
	add("ppt/theme/theme1.xml", d.themeXML("AgentGo"))

	// Master and layouts.
	add("ppt/slideMasters/slideMaster1.xml", d.masterXML(layoutCount))
	masterRels := relsHead
	for i := 1; i <= layoutCount; i++ {
		masterRels += relXML(fmt.Sprintf("rId%d", i), relBase+"slideLayout", fmt.Sprintf("../slideLayouts/slideLayout%d.xml", i))
		add(fmt.Sprintf("ppt/slideLayouts/slideLayout%d.xml", i), layoutXML(i))
		add(fmt.Sprintf("ppt/slideLayouts/_rels/slideLayout%d.xml.rels", i),
			relsHead+relXML("rId1", relBase+"slideMaster", "../slideMasters/slideMaster1.xml")+`</Relationships>`)
	}
	masterRels += relXML(fmt.Sprintf("rId%d", layoutCount+1), relBase+"theme", "../theme/theme1.xml") + `</Relationships>`
	add("ppt/slideMasters/_rels/slideMaster1.xml.rels", masterRels)
	if hasNotes {
		add("ppt/notesMasters/notesMaster1.xml", notesMasterXML())
		add("ppt/notesMasters/_rels/notesMaster1.xml.rels", relsHead+relXML("rId1", relBase+"theme", "../theme/theme2.xml")+`</Relationships>`)
		add("ppt/theme/theme2.xml", d.themeXML("AgentGo Notes"))
	}

	files = append(files, d.contentParts("../notesMasters/notesMaster1.xml")...)
	return zipFiles(files, now)
}

// contentParts are the parts the slides bring along: the slides with their
// notes, the charts with their workbooks, and the pictures. notesMaster is the
// target of the notes slides' master relationship, relative to ppt/notesSlides.
func (d *deck) contentParts(notesMaster string) []pkgFile {
	var files []pkgFile
	add := func(name, body string) { files = append(files, pkgFile{name, []byte(body)}) }
	for i, s := range d.slides {
		n := i + 1
		rels := relsHead + relXML("rId1", relBase+"slideLayout", fmt.Sprintf("../slideLayouts/slideLayout%d.xml", s.layout))
		for _, r := range s.rels {
			rels += relXML(r.id, r.typ, r.target)
		}
		if strings.TrimSpace(s.notes) != "" {
			rels += relXML(fmt.Sprintf("rId%d", len(s.rels)+2), relBase+"notesSlide", fmt.Sprintf("../notesSlides/notesSlide%d.xml", n))
			add(fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n), notesSlideXML(s.notes))
			add(fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", n),
				relsHead+relXML("rId1", relBase+"notesMaster", notesMaster)+
					relXML("rId2", relBase+"slide", fmt.Sprintf("../slides/slide%d.xml", n))+`</Relationships>`)
		}
		add(fmt.Sprintf("ppt/slides/slide%d.xml", n), s.xml)
		add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), rels+`</Relationships>`)
	}
	for i, c := range d.charts {
		n := i + 1
		add(fmt.Sprintf("ppt/charts/chart%d.xml", n), c.xml)
		add(fmt.Sprintf("ppt/charts/_rels/chart%d.xml.rels", n),
			relsHead+relXML("rId1", relPackage, fmt.Sprintf("../embeddings/Microsoft_Excel_Sheet%d.xlsx", n))+`</Relationships>`)
		files = append(files, pkgFile{fmt.Sprintf("ppt/embeddings/Microsoft_Excel_Sheet%d.xlsx", n), c.workbook})
	}
	for _, im := range d.images {
		files = append(files, pkgFile{"ppt/media/" + im.name, im.data})
	}
	return files
}

// zipFiles writes the parts as a zip package, in order.
func zipFiles(files []pkgFile, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func corePropsXML(title, author string, now time.Time) string {
	if author == "" {
		author = "AgentGo"
	}
	ts := now.Format("2006-01-02T15:04:05Z")
	return xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + escapeXML(title) + `</dc:title><dc:creator>` + escapeXML(author) + `</dc:creator>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:modified></cp:coreProperties>`
}
