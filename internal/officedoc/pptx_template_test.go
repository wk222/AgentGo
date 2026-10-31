package officedoc

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testNow() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }

func tplEnv(t *testing.T) (string, Env) {
	t.Helper()
	root := t.TempDir()
	return root, Env{Resolve: func(rel string) (string, error) {
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
			return "", errors.New("path escapes the workspace: " + rel)
		}
		return filepath.Join(root, filepath.FromSlash(rel)), nil
	}}
}

func readZipParts(t *testing.T, data []byte) (map[string][]byte, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	var order []string
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = b
		order = append(order, f.Name)
	}
	return parts, order
}

const tplLogoXML = `<p:pic><p:nvPicPr><p:cNvPr id="9" name="Logo"/><p:cNvPicPr/><p:nvPr userDrawn="1"/></p:nvPicPr><p:blipFill><a:blip r:embed="rId2"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>` +
	`<p:spPr><a:xfrm><a:off x="10240645" y="0"/><a:ext cx="1961515" cy="1000000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`

// buildCompanyTemplate writes tpl/company.pptx: a deck made by our own writer
// (cover, picture and chart slides with notes, closing) then dressed like a
// company template: the title-only layout gets a brand-coloured title and a
// logo in the top right corner, and the package gets a thumbnail and a section
// list that refer to the template's own slides.
func buildCompanyTemplate(t *testing.T, root string, env Env, logoName string) {
	t.Helper()
	writeTestPNG(t, root)
	_, err := createPptx(env, createPptxInput{
		RelativePath: "tpl/base.pptx", Title: "公司模板", Theme: "mobile", Closing: "谢谢",
		Slides: []pptxSlideIn{
			{Layout: "image", Title: "模板里的图", Image: "assets/map.png", Notes: "模板里的备注"},
			{Layout: "chart", Title: "模板里的图表", Chart: &pptxChartIn{Type: "column", Categories: []string{"a", "b"},
				Series: []pptxSeriesIn{{Name: "s", Values: []float64{1, 2}}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "tpl", "base.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	parts, order := readZipParts(t, data)
	parts["ppt/slideLayouts/slideLayout2.xml"] = []byte(strings.Replace(
		strings.Replace(string(parts["ppt/slideLayouts/slideLayout2.xml"]), `<a:lstStyle/>`,
			`<a:lstStyle><a:lvl1pPr algn="l"><a:defRPr sz="2400" b="1"><a:solidFill><a:srgbClr val="0070C0"/></a:solidFill></a:defRPr></a:lvl1pPr></a:lstStyle>`, 1),
		`</p:spTree>`, tplLogoXML+`</p:spTree>`, 1))
	parts["ppt/slideLayouts/_rels/slideLayout2.xml.rels"] = []byte(strings.Replace(string(parts["ppt/slideLayouts/_rels/slideLayout2.xml.rels"]),
		`</Relationships>`, relXML("rId2", relImage, "../media/"+logoName)+`</Relationships>`, 1))
	logo := mustRead(t, filepath.Join(root, "assets", "map.png"))
	if _, dup := parts["ppt/media/"+logoName]; !dup {
		order = append(order, "ppt/media/"+logoName)
	}
	parts["ppt/media/"+logoName] = logo
	parts["docProps/thumbnail.jpeg"] = []byte("thumb")
	order = append(order, "docProps/thumbnail.jpeg")
	parts["_rels/.rels"] = []byte(strings.Replace(string(parts["_rels/.rels"]), `</Relationships>`,
		relXML("rId9", "http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail", "docProps/thumbnail.jpeg")+`</Relationships>`, 1))
	parts["[Content_Types].xml"] = []byte(strings.Replace(string(parts["[Content_Types].xml"]), `</Types>`,
		`<Default Extension="jpeg" ContentType="image/jpeg"/></Types>`, 1))
	parts["ppt/presentation.xml"] = []byte(strings.Replace(string(parts["ppt/presentation.xml"]), `</p:presentation>`,
		`<p:extLst><p:ext uri="{521415D9-36F7-43E2-AB2F-B90AF26B5E84}"><p14:sectionLst xmlns:p14="http://schemas.microsoft.com/office/powerpoint/2010/main"><p14:section name="旧章节" id="{00000000-0000-0000-0000-000000000001}"><p14:sldIdLst><p14:sldId id="256"/></p14:sldIdLst></p14:section></p14:sectionLst></p:ext></p:extLst></p:presentation>`, 1))
	var files []pkgFile
	for _, name := range order { // [Content_Types].xml is first in order already
		files = append(files, pkgFile{name, parts[name]})
	}
	out, err := zipFiles(files, testNow())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tpl", "company.pptx"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildOnTemplate(t *testing.T, env Env, in createPptxInput) (createPptxOutput, map[string]string) {
	t.Helper()
	in.Template = "tpl/company.pptx"
	in.RelativePath = "out/new.pptx"
	in.Overwrite = true
	if in.Title == "" {
		in.Title = "新汇报"
	}
	out, err := createPptx(env, in)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := env.Resolve(in.RelativePath)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return out, checkPackage(t, data)
}

func TestTemplateKeepsLayoutsAndDropsTheTemplatesSlides(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	out, parts := buildOnTemplate(t, env, createPptxInput{
		Subtitle: "副标题", Author: "网络部", Date: "2026年10月", Closing: "谢谢",
		Slides: []pptxSlideIn{
			{Layout: "bullets", Title: "第一页", Bullets: []string{"要点一", "要点二"}, Notes: "新的备注"},
			{Layout: "svg", Title: "第二页", Svg: svgHead + `<rect x="100" y="200" width="300" height="100" fill="primary"/></svg>`},
		},
	})
	if out.Slides != 4 {
		t.Fatalf("want cover + 2 + closing = 4 slides, got %d (%v)", out.Slides, out.Outline)
	}
	tplParts, _ := readZipParts(t, mustRead(t, filepath.Join(root, "tpl", "company.pptx")))

	// Everything the layouts need is carried over untouched.
	for _, name := range []string{"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/slideLayout3.xml", "ppt/slideMasters/slideMaster1.xml", "ppt/theme/theme1.xml", "ppt/media/logo.png"} {
		if got, want := parts[name], string(tplParts[name]); got == "" || got != want {
			t.Errorf("%s was not carried over byte for byte", name)
		}
	}
	// The layout that names the logo has changed in the template, so it must match that, not our generator's.
	if parts["ppt/slideLayouts/slideLayout2.xml"] != string(tplParts["ppt/slideLayouts/slideLayout2.xml"]) {
		t.Error("layout 2 (with the logo) was altered")
	}
	// The template's own slides, and what only they used, are gone.
	for _, name := range []string{"ppt/charts/chart1.xml", "ppt/embeddings/Microsoft_Excel_Sheet1.xlsx", "ppt/media/image1.png", "docProps/thumbnail.jpeg"} {
		if _, ok := parts[name]; ok {
			t.Errorf("%s should have been dropped with the template's slides", name)
		}
	}
	for name, body := range parts {
		for _, stale := range []string{"模板里的图", "模板里的备注", "旧章节", "sectionLst", "thumbnail"} {
			if strings.Contains(body, stale) {
				t.Errorf("%s still mentions %q", name, stale)
			}
		}
	}
	if n := len(regexp.MustCompile(`<p:sldId `).FindAllString(parts["ppt/presentation.xml"], -1)); n != 4 {
		t.Errorf("presentation lists %d slides, want 4", n)
	}
	if n := strings.Count(parts["ppt/_rels/presentation.xml.rels"], "/relationships/slide\""); n != 4 {
		t.Errorf("presentation.xml.rels has %d slide relationships, want 4", n)
	}
	// Notes reuse the template's notes master instead of adding a second one.
	masters := 0
	for name := range parts {
		if strings.HasPrefix(name, "ppt/notesMasters/notesMaster") && strings.HasSuffix(name, ".xml") {
			masters++
		}
	}
	if masters != 1 {
		t.Errorf("want exactly one notes master, found %d", masters)
	}
	if !strings.Contains(parts["ppt/notesSlides/notesSlide2.xml"], "新的备注") {
		t.Error("the new slide's notes are missing")
	}
	if !strings.Contains(parts["ppt/notesSlides/_rels/notesSlide2.xml.rels"], "notesMasters/notesMaster1.xml") {
		t.Errorf("notes do not point at the template's notes master: %s", parts["ppt/notesSlides/_rels/notesSlide2.xml.rels"])
	}
	// Cover on the cover layout, content on the layout most sample slides used.
	for n, layout := range map[string]string{"1": "slideLayout1.xml", "2": "slideLayout2.xml", "3": "slideLayout2.xml", "4": "slideLayout1.xml"} {
		if rel := parts["ppt/slides/_rels/slide"+n+".xml.rels"]; !strings.Contains(rel, "../slideLayouts/"+layout) {
			t.Errorf("slide %s: want layout %s, rels: %s", n, layout, rel)
		}
	}
}

func TestTemplateSlidesUseThePlaceholdersOfTheLayout(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	_, parts := buildOnTemplate(t, env, createPptxInput{
		Subtitle: "副标题", Author: "网络部", Date: "2026年10月",
		Slides: []pptxSlideIn{
			{Layout: "bullets", Title: "第一页的结论", Bullets: []string{"要点一"}},
			{Layout: "stats", Title: "第二页", Stats: []pptxStatIn{{Value: "9%", Label: "a"}, {Value: "8%", Label: "b"}}},
		},
	})
	slide := parts["ppt/slides/slide2.xml"]
	if !strings.Contains(slide, `<p:ph type="title"/>`) {
		t.Fatalf("the title is not the title placeholder:\n%s", slide)
	}
	// The title inherits position and formatting: no xfrm and no explicit run formatting in its shape.
	title := ""
	for _, sp := range strings.Split(slide, "<p:sp>") {
		if strings.Contains(sp, `type="title"`) {
			title = sp[:strings.Index(sp, "</p:sp>")]
		}
	}
	if title == "" {
		t.Fatal("no title shape found")
	}
	for _, own := range []string{"<a:xfrm>", "sz=", "srgbClr", "latin"} {
		if strings.Contains(title, own) {
			t.Errorf("the title sets %s itself instead of inheriting it from the layout: %s", own, title)
		}
	}
	// Our own title rule, footer and slide-number box would double up with the template's.
	for _, own := range []string{"Deco rule", "Deco footer line", `name="Footer"`, `name="Slide Number"`} {
		if strings.Contains(slide, own) {
			t.Errorf("template slide carries %q", own)
		}
	}
	// The layout has no slide-number placeholder, so none is added to its slides.
	if strings.Contains(slide, `type="sldNum"`) {
		t.Error("a slide number placeholder was added although the layout has none")
	}
	// Bullets are drawn as a text box in the layout's content area, not as a body placeholder.
	if strings.Contains(slide, `idx="1"`) {
		t.Errorf("bullets use the body placeholder of a layout that has none:\n%s", slide)
	}
	if !strings.Contains(slide, "要点一") {
		t.Error("bullet text missing")
	}
}

func TestTemplateCoverIsStyledOnlyWhenTheLayoutLeavesItPlain(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	// Layout 1 (cover) sets no colour: the cover title gets bold brand-colour text. Layout 2 sets its
	// own colour: used as a cover it must be left alone.
	_, parts := buildOnTemplate(t, env, createPptxInput{
		Subtitle: "副标题",
		Slides:   []pptxSlideIn{{Layout: "section", Title: "第一部分", Body: "说明", TemplateLayout: "2"}},
	})
	cover := parts["ppt/slides/slide1.xml"]
	for _, want := range []string{`sz="5400"`, `b="1"`, `val="0070C0"`, `type="ctrTitle"`, `type="subTitle"`} {
		if !strings.Contains(cover, want) {
			t.Errorf("cover lacks %s:\n%s", want, cover)
		}
	}
	section := parts["ppt/slides/slide2.xml"]
	if !strings.Contains(section, `type="title"`) || strings.Contains(section, "<a:srgbClr") {
		t.Errorf("a layout that styles its own title must be left to do so:\n%s", section)
	}
	if !strings.Contains(section, "第一部分") {
		t.Error("section title missing")
	}
}

func TestTemplatePaletteFollowsTheBrandColour(t *testing.T) {
	p := brandPalette("0070C0")
	if p.Secondary != "0070C0" || p.Chart[0] != "0070C0" {
		t.Errorf("brand colour not used: %+v", p)
	}
	if p.Primary == p.Secondary || p.Surface == "F2F5FA" {
		t.Errorf("the dependent colours were not derived: %+v", p)
	}
	if palettes["blue"].Secondary == "0070C0" || palettes["blue"].Chart[0] == "0070C0" {
		t.Error("brandPalette changed the shared blue palette")
	}
	if validBrand("FFFFFF") || validBrand("zzzzzz") || !validBrand("0070C0") {
		t.Error("validBrand")
	}

	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	_, parts := buildOnTemplate(t, env, createPptxInput{
		Slides: []pptxSlideIn{{Layout: "stats", Title: "关键数字", Stats: []pptxStatIn{{Value: "9%", Label: "a"}, {Value: "8%", Label: "b"}}}},
	})
	if !strings.Contains(parts["ppt/slides/slide2.xml"], `val="0070C0"`) {
		t.Error("generated shapes do not use the template's brand colour")
	}
	// An explicit theme still wins.
	_, parts = buildOnTemplate(t, env, createPptxInput{Theme: "green",
		Slides: []pptxSlideIn{{Layout: "stats", Title: "关键数字", Stats: []pptxStatIn{{Value: "9%", Label: "a"}, {Value: "8%", Label: "b"}}}},
	})
	if strings.Contains(parts["ppt/slides/slide2.xml"], `val="0070C0"`) || !strings.Contains(parts["ppt/slides/slide2.xml"], palettes["green"].Secondary) {
		t.Error("theme=green was ignored in template mode")
	}
}

func TestTemplateSVGSlides(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	out, parts := buildOnTemplate(t, env, createPptxInput{
		Slides: []pptxSlideIn{
			// The heading in the svg is dropped (the title placeholder carries it); the band over the logo is reported.
			{Layout: "svg", Title: "流程图", Svg: svgHead + `<text id="title" x="60" y="80" font-size="32">流程图</text>` +
				`<rect x="1000" y="0" width="240" height="90" fill="accent"/><rect x="100" y="300" width="300" height="100" fill="primary"/></svg>`},
		},
	})
	slide := parts["ppt/slides/slide2.xml"]
	if n := strings.Count(slide, `type="title"`); n != 1 {
		t.Errorf("want exactly one title placeholder, got %d", n)
	}
	if strings.Count(slide, "流程图") != 1 {
		t.Errorf("the svg heading was not dropped:\n%s", slide)
	}
	var sawDrop, sawOverlap bool
	for _, w := range out.Warnings {
		sawDrop = sawDrop || strings.Contains(w, `id="title"`)
		sawOverlap = sawOverlap || strings.Contains(w, `overlaps the template's picture "Logo"`)
	}
	if !sawDrop || !sawOverlap {
		t.Errorf("want warnings about the dropped heading and the logo overlap, got %v", out.Warnings)
	}
}

func TestTemplatePicksLayoutsByNumberAndName(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	tpl, err := loadTemplate(env, "tpl/company.pptx")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.cover == nil || tpl.cover.N != 1 || tpl.content == nil || tpl.content.N != 2 {
		t.Fatalf("defaults: cover %v content %v, want 1 and 2 (from the template's own slides)", tpl.cover, tpl.content)
	}
	if tpl.brand != "0070C0" {
		t.Errorf("brand colour %q", tpl.brand)
	}
	for spec, want := range map[string]int{"3": 3, "L3": 3, "layout3": 3, "Title Only": 2, "Title Slide": 1} {
		l, err := tpl.pick(spec, nil)
		if err != nil || l.N != want {
			t.Errorf("pick(%q) = %v, %v; want layout %d", spec, l, err, want)
		}
	}
	if l, _ := tpl.pick("", tpl.content); l != tpl.content {
		t.Error("an empty spec must give the default")
	}
	if _, err := tpl.pick("", nil); err == nil {
		t.Error("an empty spec without a default must fail")
	}
	for _, bad := range []string{"99", "No Such Layout"} {
		if _, err := tpl.pick(bad, nil); err == nil {
			t.Errorf("pick(%q) should fail", bad)
		}
	}
	// A name carried by several layouts is ambiguous unless the template's own slides settle it.
	l2 := *tpl.layouts[2]
	l2.N, l2.Used = 20, nil
	tpl.layouts[20] = &l2
	tpl.sorted = append(tpl.sorted, &l2)
	if l, err := tpl.pick("Title Only", nil); err != nil || l.N != 2 {
		t.Errorf("the layout the sample slides use should win, got %v, %v", l, err)
	}
	tpl.layouts[2].Used = nil
	if _, err := tpl.pick("Title Only", nil); err == nil || !strings.Contains(err.Error(), "2, 20") {
		t.Errorf("an ambiguous name must list the numbers, got %v", err)
	}
}

func TestTemplateDefaultsWithoutSampleSlides(t *testing.T) {
	mk := func(n int, kind string, phs ...string) *tplLayout {
		l := &tplLayout{N: n, Name: kind, Kind: kind, Master: 1}
		for _, p := range phs {
			l.PH = append(l.PH, tplPH{Type: p})
		}
		return l
	}
	tpl := &template{layouts: map[int]*tplLayout{}}
	for _, l := range []*tplLayout{mk(1, "secHead", "title"), mk(2, "title", "ctrTitle", "subTitle"), mk(3, "obj", "title", ""), mk(4, "titleOnly", "title")} {
		tpl.layouts[l.N] = l
		tpl.sorted = append(tpl.sorted, l)
	}
	tpl.chooseDefaults()
	if tpl.cover.N != 2 || tpl.content.N != 4 {
		t.Errorf("by layout type: cover %d content %d, want 2 and 4", tpl.cover.N, tpl.content.N)
	}
}

func TestInspectListsTheLayoutsOfATemplate(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	out, err := inspectPptx(env, inspectPptxInput{RelativePath: "tpl/company.pptx", Layouts: true, Slides: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Template == nil || len(out.Template.Layout) < 2 {
		t.Fatalf("no layouts listed: %+v", out.Template)
	}
	byID := map[string]tplLayoutInfo{}
	for _, l := range out.Template.Layout {
		byID[l.ID] = l
	}
	l2, ok := byID["2"]
	if !ok || l2.Name != "Title Only" || l2.DefaultFor != "content" {
		t.Fatalf("layout 2: %+v", l2)
	}
	if len(l2.UsedBy) != 2 {
		t.Errorf("layout 2 is used by the template's slides 2 and 3, got %v", l2.UsedBy)
	}
	if !strings.Contains(strings.Join(l2.Decor, "|"), `picture "Logo" x=1075`) {
		t.Errorf("the logo is not listed with its position in svg px: %v", l2.Decor)
	}
	if !strings.HasPrefix(l2.ContentBox, "x=") || !strings.Contains(strings.Join(l2.Placeholders, "|"), "title x=") {
		t.Errorf("layout 2 geometry: %+v", l2)
	}
	if out.Template.Brand != "0070C0" {
		t.Errorf("brand %q", out.Template.Brand)
	}
	if !strings.Contains(out.Template.Canvas, "1280 720") {
		t.Errorf("canvas note %q", out.Template.Canvas)
	}
}

func TestTemplateErrors(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	one := []pptxSlideIn{{Layout: "bullets", Title: "页", Bullets: []string{"点"}}}
	cases := []struct {
		name string
		in   createPptxInput
		want string
	}{
		{"missing template", createPptxInput{Template: "tpl/nope.pptx", RelativePath: "o.pptx", Title: "x", Slides: one}, "cannot open template"},
		{"not a pptx", createPptxInput{Template: "assets/map.png", RelativePath: "o.pptx", Title: "x", Slides: one}, "must be a .pptx or .potx"},
		{"overwrite the template", createPptxInput{Template: "tpl/company.pptx", RelativePath: "tpl/company.pptx", Overwrite: true, Title: "x", Slides: one}, "template itself"},
		{"unknown layout", createPptxInput{Template: "tpl/company.pptx", RelativePath: "o.pptx", Title: "x", Slides: []pptxSlideIn{{Layout: "bullets", Title: "页", Bullets: []string{"点"}, TemplateLayout: "42"}}}, "no layout number 42"},
		{"bad cover layout", createPptxInput{Template: "tpl/company.pptx", RelativePath: "o.pptx", Title: "x", CoverLayout: "Nope", Slides: one}, "cover_layout"},
	}
	for _, c := range cases {
		_, err := createPptx(env, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	// A layout without a title placeholder cannot take the built-in layouts; svg is the way.
	tpl, _ := loadTemplate(env, "tpl/company.pptx")
	tpl.layouts[2].PH = nil
	d := &deck{tpl: tpl, tplContent: tpl.layouts[2], tplCover: tpl.layouts[1], pal: palettes["blue"], font: defaultFont, fr: defaultFrame, env: env}
	if _, err := d.buildSlide(pptxSlideIn{Layout: "bullets", Title: "页", Bullets: []string{"点"}}); err == nil || !strings.Contains(err.Error(), "no title placeholder") {
		t.Errorf("want the no-title-placeholder error, got %v", err)
	}
	built, err := d.buildSlide(pptxSlideIn{Layout: "svg", Title: "页", Svg: svgHead + `<text id="title" x="60" y="80" font-size="32">没有占位符的标题</text></svg>`})
	if err != nil {
		t.Fatalf("svg must work on a layout without a title placeholder: %v", err)
	}
	// With nowhere to put a title the heading stays an ordinary text box.
	if x := built[0].xml; !strings.Contains(x, "没有占位符的标题") || strings.Contains(x, `type="title"`) {
		t.Errorf("heading must stay plain text on a layout without a title placeholder:\n%s", x)
	}
}

func TestTemplateRenamesPicturesThatCollideWithItsMedia(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "image1.png") // the logo takes the name our first picture would get
	_, parts := buildOnTemplate(t, env, createPptxInput{
		Slides: []pptxSlideIn{{Layout: "image", Title: "新图", Image: "assets/map.png"}},
	})
	logo := parts["ppt/media/image1.png"]
	if logo == "" || parts["ppt/media/agentgo1-image1.png"] == "" {
		var names []string
		for n := range parts {
			if strings.HasPrefix(n, "ppt/media/") {
				names = append(names, n)
			}
		}
		t.Fatalf("want the template's image1.png and our agentgo1-image1.png, media: %v", names)
	}
	if !strings.Contains(parts["ppt/slides/_rels/slide2.xml.rels"], "../media/agentgo1-image1.png") {
		t.Errorf("slide does not point at the renamed picture: %s", parts["ppt/slides/_rels/slide2.xml.rels"])
	}
	if !strings.Contains(parts["ppt/slideLayouts/_rels/slideLayout2.xml.rels"], "../media/image1.png") {
		t.Error("the layout lost its logo")
	}
}

func TestTemplateWithoutNotesMasterGetsOne(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	// Take the notes master out of the template (and the slides that had notes with it).
	data := mustRead(t, filepath.Join(root, "tpl", "company.pptx"))
	parts, order := readZipParts(t, data)
	var files []pkgFile
	for _, name := range order {
		switch {
		case strings.HasPrefix(name, "ppt/notesMasters/"), name == "ppt/theme/theme2.xml", strings.HasPrefix(name, "ppt/notesSlides/"):
			continue
		}
		body := parts[name]
		switch name {
		case "ppt/presentation.xml":
			body = regexp.MustCompile(`<p:notesMasterIdLst>.*?</p:notesMasterIdLst>`).ReplaceAll(body, nil)
		case "ppt/_rels/presentation.xml.rels":
			body = regexp.MustCompile(`<Relationship [^>]*notesMaster[^>]*/>`).ReplaceAll(body, nil)
		case "[Content_Types].xml":
			body = regexp.MustCompile(`<Override PartName="/ppt/(notesMasters|theme/theme2|notesSlides)[^>]*/>`).ReplaceAll(body, nil)
		}
		if strings.HasPrefix(name, "ppt/slides/_rels/") {
			body = regexp.MustCompile(`<Relationship [^>]*notesSlide[^>]*/>`).ReplaceAll(body, nil)
		}
		files = append(files, pkgFile{name, body})
	}
	out, err := zipFiles(files, testNow())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tpl", "company.pptx"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	_, p := buildOnTemplate(t, env, createPptxInput{
		Slides: []pptxSlideIn{{Layout: "bullets", Title: "页", Bullets: []string{"点"}, Notes: "有备注"}},
	})
	if !strings.Contains(p["ppt/presentation.xml"], "<p:notesMasterIdLst>") {
		t.Error("presentation.xml does not register the notes master")
	}
	if p["ppt/notesMasters/notesMaster1.xml"] == "" || p["ppt/theme/theme2.xml"] == "" {
		t.Error("our notes master and its theme were not added")
	}
	if !strings.Contains(p["ppt/notesSlides/notesSlide2.xml"], "有备注") {
		t.Error("notes missing")
	}
	// notesMasterIdLst sits between the master list and the slide list, as the schema orders them.
	pres := p["ppt/presentation.xml"]
	a, b, c := strings.Index(pres, "</p:sldMasterIdLst>"), strings.Index(pres, "<p:notesMasterIdLst>"), strings.Index(pres, "<p:sldIdLst>")
	if !(a >= 0 && a < b && b < c) {
		t.Errorf("element order is wrong (%d, %d, %d): %s", a, b, c, pres)
	}
}

func TestTemplateFromAPotxIsStillAPresentation(t *testing.T) {
	root, env := tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	parts, order := readZipParts(t, mustRead(t, filepath.Join(root, "tpl", "company.pptx")))
	ct := string(parts["[Content_Types].xml"])
	if !strings.Contains(ct, pmlPrefix+"presentation.main+xml") {
		t.Fatalf("fixture does not declare a presentation main part:\n%s", ct)
	}
	parts["[Content_Types].xml"] = []byte(strings.Replace(ct, pmlPrefix+"presentation.main+xml", pmlPrefix+"template.main+xml", 1))
	var files []pkgFile
	for _, name := range order {
		files = append(files, pkgFile{name, parts[name]})
	}
	data, err := zipFiles(files, testNow())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tpl", "company.potx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createPptx(env, createPptxInput{Template: "tpl/company.potx", RelativePath: "out/from-potx.pptx", Title: "来自 potx",
		Slides: []pptxSlideIn{{Layout: "bullets", Title: "页", Bullets: []string{"点"}}}}); err != nil {
		t.Fatal(err)
	}
	out := checkPackage(t, mustRead(t, filepath.Join(root, "out", "from-potx.pptx")))
	if got := out["[Content_Types].xml"]; strings.Contains(got, "template.main+xml") || !strings.Contains(got, pmlPrefix+"presentation.main+xml") {
		t.Errorf("the deck made from a .potx must be a presentation, not a template:\n%s", got)
	}
}
