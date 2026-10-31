package officedoc

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// companyDeckWithHistory is buildCompanyTemplate's deck plus what a real
// company deck carries around: comment authors, revision history, a company
// name and an author in the document properties.
func companyDeckWithHistory(t *testing.T) (root string, env Env, src []byte) {
	t.Helper()
	root, env = tplEnv(t)
	buildCompanyTemplate(t, root, env, "logo.png")
	parts, order := readZipParts(t, mustRead(t, filepath.Join(root, "tpl", "company.pptx")))
	parts["ppt/commentAuthors.xml"] = []byte(xmlHeader + `<p:cmAuthorLst xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cmAuthor id="0" name="张三" initials="ZS" lastIdx="1" clrIdx="0"/></p:cmAuthorLst>`)
	parts["ppt/revisionInfo.xml"] = []byte(xmlHeader + `<p:revInfo xmlns:p="http://schemas.microsoft.com/office/powerpoint/2015/10/main"><p:revLst><p:client id="{1}" v="1" author="张三"/></p:revLst></p:revInfo>`)
	parts["docProps/app.xml"] = []byte(xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Company>某某公司</Company><Slides>4</Slides></Properties>`)
	parts["docProps/core.xml"] = []byte(corePropsXML("某某公司年度汇报", "张三", testNow()))
	parts["ppt/tags/tag1.xml"] = []byte(xmlHeader + `<p:tagLst xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:tag name="KSO_WPP_MARK_KEY" val="23f2bbba"/><p:tag name="COMMONDATA" val="eyJoZGlkIjoiTU9DSyJ9"/></p:tagLst>`)
	order = append(order, "ppt/commentAuthors.xml", "ppt/revisionInfo.xml", "ppt/tags/tag1.xml")
	parts["[Content_Types].xml"] = []byte(strings.Replace(string(parts["[Content_Types].xml"]), `</Types>`,
		`<Override PartName="/ppt/commentAuthors.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.commentAuthors+xml"/>`+
			`<Override PartName="/ppt/revisionInfo.xml" ContentType="application/vnd.ms-powerpoint.revisioninfo+xml"/>`+
			`<Override PartName="/ppt/tags/tag1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tags+xml"/></Types>`, 1))
	parts["ppt/_rels/presentation.xml.rels"] = []byte(strings.Replace(string(parts["ppt/_rels/presentation.xml.rels"]), `</Relationships>`,
		relXML("rId90", relBase+"commentAuthors", "commentAuthors.xml")+
			relXML("rId91", "http://schemas.microsoft.com/office/2015/10/relationships/revisionInfo", "revisionInfo.xml")+
			relXML("rId92", relBase+"tags", "tags/tag1.xml")+`</Relationships>`, 1))
	var files []pkgFile
	for _, name := range order {
		files = append(files, pkgFile{name, parts[name]})
	}
	src, err := zipFiles(files, testNow())
	if err != nil {
		t.Fatal(err)
	}
	return root, env, src
}

func TestStripTemplateRemovesTheContentButKeepsTheLook(t *testing.T) {
	_, _, src := companyDeckWithHistory(t)
	out, err := stripTemplate(src, "Template")
	if err != nil {
		t.Fatal(err)
	}
	got := checkPackage(t, out) // well-formed: content types, relationship targets
	srcParts, _ := readZipParts(t, src)

	// Nothing of the deck's own content or of its authors.
	for _, name := range []string{"ppt/commentAuthors.xml", "ppt/revisionInfo.xml", "ppt/charts/chart1.xml", "ppt/embeddings/Microsoft_Excel_Sheet1.xlsx",
		"ppt/media/image1.png", "docProps/thumbnail.jpeg"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s must not be in a built-in template", name)
		}
	}
	for name, body := range got {
		if strings.HasPrefix(name, "ppt/notesSlides/") {
			t.Errorf("%s: notes must be gone", name)
		}
		for _, leak := range []string{"张三", "某某公司", "模板里的图", "模板里的备注", "旧章节", "sectionLst", "commentAuthors", "revisionInfo", "COMMONDATA", "eyJoZGlk", "KSO_WPP"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s still mentions %q", name, leak)
			}
		}
	}
	// The tag part stays (things point at it) but says nothing.
	if tags := got["ppt/tags/tag1.xml"]; !strings.Contains(tags, "<p:tagLst") || strings.Contains(tags, "<p:tag ") {
		t.Errorf("tag part should be an empty list:\n%s", tags)
	}
	// The look is untouched.
	for _, name := range []string{"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/slideLayout2.xml",
		"ppt/slideLayouts/slideLayout3.xml", "ppt/theme/theme1.xml", "ppt/media/logo.png"} {
		if got[name] == "" || got[name] != string(srcParts[name]) {
			t.Errorf("%s was not carried over byte for byte", name)
		}
	}
	// Slides are empty stand-ins on the same layouts, in the same order.
	wantLayout := []string{"slideLayout1", "slideLayout2", "slideLayout2", "slideLayout1"}
	for i, want := range wantLayout {
		n := i + 1
		slide := got["ppt/slides/slide"+strconv.Itoa(n)+".xml"]
		if slide == "" || strings.Contains(slide, "<p:sp>") || strings.Contains(slide, "<p:pic>") || strings.Contains(slide, "<a:t>") {
			t.Errorf("slide %d is not empty:\n%s", n, slide)
		}
		if rels := got["ppt/slides/_rels/slide"+strconv.Itoa(n)+".xml.rels"]; !strings.Contains(rels, want+".xml") {
			t.Errorf("slide %d should sit on %s:\n%s", n, want, rels)
		}
	}
	// Reading it back gives the same cover, content layout and brand colour as the source.
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := templateFromZip(zr.File, "x", "x")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.cover == nil || tpl.cover.N != 1 || tpl.content == nil || tpl.content.N != 2 || tpl.brand != "0070C0" {
		t.Errorf("defaults changed: cover=%v content=%v brand=%q", tpl.cover, tpl.content, tpl.brand)
	}
	if len(tpl.sorted) != 3 || len(tpl.samples) != 4 {
		t.Errorf("want 3 layouts and 4 stand-in slides, got %d and %d", len(tpl.sorted), len(tpl.samples))
	}
}

func withBuiltin(t *testing.T, files map[string][]byte) {
	t.Helper()
	m := fstest.MapFS{}
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: data}
	}
	old := builtinTemplates
	builtinTemplates = m
	t.Cleanup(func() { builtinTemplates = old })
}

func TestBuiltinTemplateWorksLikeAWorkspaceTemplate(t *testing.T) {
	_, env, src := companyDeckWithHistory(t)
	stripped, err := stripTemplate(src, "Template")
	if err != nil {
		t.Fatal(err)
	}
	withBuiltin(t, map[string][]byte{"corp.pptx": stripped, "README.txt": []byte("x")})
	if got := builtinNames(); len(got) != 1 || got[0] != "corp" {
		t.Fatalf("builtinNames = %v", got)
	}

	// inspect_pptx reads its layouts, and says what the slides are.
	in, err := inspectPptx(env, inspectPptxInput{RelativePath: "builtin:corp"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Path != "builtin:corp" || in.Template == nil || len(in.Items) != 0 || len(in.Issues) != 0 {
		t.Fatalf("inspect of a built-in template: %+v", in)
	}
	if !strings.Contains(strings.Join(in.Notes, " "), "built-in template") {
		t.Errorf("notes should explain the stand-in slides: %v", in.Notes)
	}
	roles := map[string]string{}
	for _, l := range in.Template.Layout {
		roles[l.ID] = l.DefaultFor
	}
	if roles["1"] != "cover, closing" || roles["2"] != "content" || in.Template.Brand != "0070C0" {
		t.Errorf("defaults lost: %v brand=%s", roles, in.Template.Brand)
	}

	// create_pptx builds on it, whatever the case of the name.
	out, err := createPptx(env, createPptxInput{Template: "builtin:CORP", RelativePath: "out/b.pptx", Title: "汇报", Closing: "谢谢",
		Slides: []pptxSlideIn{
			{Layout: "bullets", Title: "第一页", Bullets: []string{"要点"}},
			{Layout: "svg", Title: "第二页", Svg: svgHead + `<rect x="100" y="200" width="300" height="100" fill="primary"/></svg>`},
		}})
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := env.Resolve("out/b.pptx")
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	parts := checkPackage(t, data)
	if out.Slides != 4 {
		t.Fatalf("slides = %d, want 4", out.Slides)
	}
	strippedParts, _ := readZipParts(t, stripped)
	for _, name := range []string{"ppt/slideLayouts/slideLayout2.xml", "ppt/slideMasters/slideMaster1.xml", "ppt/media/logo.png"} {
		if parts[name] == "" || parts[name] != string(strippedParts[name]) {
			t.Errorf("%s differs from the built-in template", name)
		}
	}
	if rels := parts["ppt/slides/_rels/slide2.xml.rels"]; !strings.Contains(rels, "slideLayout2.xml") {
		t.Errorf("content slide should use the template's content layout:\n%s", rels)
	}
	if strings.Contains(parts["docProps/core.xml"], "某某公司") {
		t.Error("the built-in template's own properties leaked into the deck")
	}
}

func TestBuiltinTemplateErrors(t *testing.T) {
	_, env, src := companyDeckWithHistory(t)
	stripped, err := stripTemplate(src, "Template")
	if err != nil {
		t.Fatal(err)
	}
	one := []pptxSlideIn{{Layout: "bullets", Title: "页", Bullets: []string{"点"}}}
	cases := []struct {
		name  string
		files map[string][]byte
		ref   string
		want  string
	}{
		{"unknown name lists the real ones", map[string][]byte{"corp.pptx": stripped}, "builtin:nope", "built-in: corp"},
		{"none in this build", nil, "builtin:corp", "none in this build"},
		{"path in the name", map[string][]byte{"corp.pptx": stripped}, "builtin:../corp", "not a built-in template name"},
		{"empty name", map[string][]byte{"corp.pptx": stripped}, "builtin:", "not a built-in template name"},
		{"damaged file", map[string][]byte{"corp.pptx": []byte("junk")}, "builtin:corp", "damaged"},
	}
	for _, c := range cases {
		withBuiltin(t, c.files)
		if _, err := createPptx(env, createPptxInput{Template: c.ref, RelativePath: "o.pptx", Title: "x", Slides: one}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: create_pptx error = %v, want %q", c.name, err, c.want)
		}
		if _, err := inspectPptx(env, inspectPptxInput{RelativePath: c.ref}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: inspect_pptx error = %v, want %q", c.name, err, c.want)
		}
	}
}

// Whatever templates this build carries must work end to end. A checkout
// without them (they are not in the repository) has nothing to test.
func TestEmbeddedBuiltinTemplatesWork(t *testing.T) {
	names := builtinNames()
	if len(names) == 0 {
		t.Skip("this build has no built-in templates")
	}
	for _, name := range names {
		_, env := tplEnv(t)
		info, err := inspectPptx(env, inspectPptxInput{RelativePath: "builtin:" + name})
		if err != nil || info.Template == nil || len(info.Template.Layout) == 0 {
			t.Fatalf("%s: inspect: %v %+v", name, err, info.Template)
		}
		out, err := createPptx(env, createPptxInput{Template: "builtin:" + name, RelativePath: "o.pptx", Title: "测试汇报", Subtitle: "副标题", Closing: "谢谢",
			Slides: []pptxSlideIn{
				{Layout: "bullets", Title: "要点页", Bullets: []string{"第一条", "第二条"}},
				{Layout: "stats", Title: "数字页", Stats: []pptxStatIn{{Value: "98.6%", Label: "覆盖率"}, {Value: "2 起", Label: "故障"}}},
				{Layout: "svg", Title: "图形页", Svg: svgHead + `<rect x="100" y="200" width="300" height="100" fill="primary"/></svg>`},
			}})
		if err != nil {
			t.Fatalf("%s: create: %v", name, err)
		}
		if out.Slides != 5 {
			t.Errorf("%s: slides = %d", name, out.Slides)
		}
		abs, _ := env.Resolve("o.pptx")
		parts := checkPackage(t, mustRead(t, abs))
		for n, body := range parts {
			if strings.Contains(body, "测试汇报") && !strings.HasPrefix(n, "ppt/slides/") && !strings.HasPrefix(n, "docProps/") {
				t.Errorf("%s: %s", name, n)
			}
		}
	}
}

var nameOK = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// TestMakeBuiltinTemplate writes builtin_tpl/<name>.pptx from a real company
// deck; see builtin_tpl/README.txt. It does nothing unless BUILTIN_TPL_SRC is set.
func TestMakeBuiltinTemplate(t *testing.T) {
	srcPath := os.Getenv("BUILTIN_TPL_SRC")
	if srcPath == "" {
		t.Skip("set BUILTIN_TPL_SRC to the company deck to make a built-in template from")
	}
	name := strings.ToLower(strings.TrimSpace(os.Getenv("BUILTIN_TPL_NAME")))
	if name == "" {
		name = "company"
	}
	if !nameOK.MatchString(name) {
		t.Fatalf("BUILTIN_TPL_NAME %q must be lower-case letters, digits, - or _", name)
	}
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stripTemplate(src, "Template")
	if err != nil {
		t.Fatal(err)
	}
	checkPackage(t, out)
	// The result must read back with the same layouts and the same sample layouts as the source.
	read := func(data []byte) *template {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		tpl, err := templateFromZip(zr.File, "x", "x")
		if err != nil {
			t.Fatal(err)
		}
		return tpl
	}
	a, b := read(src), read(out)
	if len(a.sorted) != len(b.sorted) || len(a.samples) != len(b.samples) || a.brand != b.brand ||
		(a.cover == nil) != (b.cover == nil) || a.cover != nil && a.cover.N != b.cover.N || a.content.N != b.content.N {
		t.Fatalf("the stripped template reads differently from the source")
	}
	dst := filepath.Join("builtin_tpl", name+".pptx")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d bytes from %d bytes; %d layouts, %d stand-in slides, cover L%d, content L%d, brand %s",
		dst, len(out), len(src), len(b.sorted), len(b.samples), b.cover.N, b.content.N, b.brand)
}
