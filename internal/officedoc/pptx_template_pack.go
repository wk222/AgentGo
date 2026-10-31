package officedoc

import (
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Package assembly for template mode. Everything the new slides can reach
// (masters, layouts, themes, the pictures on them, fonts, tags) is copied from
// the template byte for byte; the template's own slides, and whatever only
// they used (photos, charts, notes, comments), are left out.

type tplRel struct {
	ID, Type, Raw, Target, Mode string // Target is the resolved part name
}

func parseRels(data []byte, baseDir string) []tplRel {
	var doc struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := make([]tplRel, 0, len(doc.Rel))
	for _, r := range doc.Rel {
		target := r.Target
		switch {
		case r.Mode == "External":
		case strings.HasPrefix(target, "/"):
			target = strings.TrimPrefix(target, "/")
		default:
			target = path.Join(baseDir, target)
		}
		out = append(out, tplRel{ID: r.ID, Type: r.Type, Raw: r.Target, Target: target, Mode: r.Mode})
	}
	return out
}

func relsFileFor(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// regenerated parts are written fresh instead of copied.
var tplRegenerated = map[string]bool{
	"[Content_Types].xml":             true,
	"_rels/.rels":                     true,
	"ppt/presentation.xml":            true,
	"ppt/_rels/presentation.xml.rels": true,
	"docProps/core.xml":               true,
	"docProps/app.xml":                true,
}

// reachable returns the parts to carry over: everything reachable from the
// package root without going through the template's slides.
func (t *template) reachable() map[string]bool {
	keep := map[string]bool{}
	var visit func(part string)
	visit = func(part string) {
		if keep[part] {
			return
		}
		if _, ok := t.parts[part]; !ok {
			return
		}
		keep[part] = true
		rf := relsFileFor(part)
		if _, ok := t.parts[rf]; !ok {
			return
		}
		keep[rf] = true
		for _, r := range parseRels(t.parts[rf], path.Dir(part)) {
			if r.Mode == "External" {
				continue
			}
			if part == "ppt/presentation.xml" && r.Type == relBase+"slide" {
				continue
			}
			visit(r.Target)
		}
	}
	for _, r := range parseRels(t.parts["_rels/.rels"], ".") {
		if r.Mode == "External" || strings.HasSuffix(r.Type, "/thumbnail") || strings.HasSuffix(r.Type, "/custom-properties") {
			continue
		}
		visit(r.Target)
	}
	visit("ppt/presentation.xml")
	return keep
}

var (
	sldIdLstRE   = regexp.MustCompile(`(?s)<p:sldIdLst\s*/>|<p:sldIdLst>.*?</p:sldIdLst>`)
	custShowRE   = regexp.MustCompile(`(?s)<p:custShowLst>.*?</p:custShowLst>`)
	sectionExtRE = regexp.MustCompile(`(?s)<p:ext uri="\{521415D9-36F7-43E2-AB2F-B90AF26B5E84\}">.*?</p:ext>`)
)

// rewritePresentation swaps the template's slide list for ours. notesRID is
// set when a notes master has to be registered (the template had none).
func rewritePresentation(src string, slideIDs []string, notesRID string) (string, error) {
	src = sldIdLstRE.ReplaceAllString(src, "")
	src = custShowRE.ReplaceAllString(src, "")
	src = sectionExtRE.ReplaceAllString(src, "")
	var list strings.Builder
	list.WriteString(`<p:sldIdLst>`)
	for i, rid := range slideIDs {
		fmt.Fprintf(&list, `<p:sldId id="%d" r:id="%s"/>`, 256+i, rid)
	}
	list.WriteString(`</p:sldIdLst>`)
	insertAfter := func(tag, body string) bool {
		i := strings.Index(src, tag)
		if i < 0 {
			return false
		}
		i += len(tag)
		src = src[:i] + body + src[i:]
		return true
	}
	if notesRID != "" {
		if !insertAfter(`</p:sldMasterIdLst>`, `<p:notesMasterIdLst><p:notesMasterId r:id="`+notesRID+`"/></p:notesMasterIdLst>`) {
			return "", errors.New("template presentation.xml has no slide master list")
		}
	}
	for _, tag := range []string{`</p:handoutMasterIdLst>`, `</p:notesMasterIdLst>`, `</p:sldMasterIdLst>`} {
		if insertAfter(tag, list.String()) {
			return src, nil
		}
	}
	return "", errors.New("template presentation.xml has no slide master list")
}

// packTemplate assembles the package in template mode.
func (d *deck) packTemplate(title, author string) ([]byte, error) {
	t := d.tpl
	now := time.Now().UTC()
	keep := t.reachable()
	hasNotes := false
	for _, s := range d.slides {
		if strings.TrimSpace(s.notes) != "" {
			hasNotes = true
		}
	}

	for _, s := range d.slides {
		if _, ok := t.layouts[s.layout]; !ok {
			return nil, fmt.Errorf("internal error: slide uses layout %d which the template does not have", s.layout)
		}
	}

	// Pictures keep their names unless the template already has that name.
	for i := range d.images {
		old := d.images[i].name
		name := old
		for n := 1; keep["ppt/media/"+name]; n++ {
			name = fmt.Sprintf("agentgo%d-%s", n, old)
		}
		if name != old {
			d.images[i].name = name
			for _, s := range d.slides {
				for j := range s.rels {
					if s.rels[j].target == "../media/"+old {
						s.rels[j].target = "../media/" + name
					}
				}
			}
		}
	}
	for i := range d.charts {
		for _, p := range []string{fmt.Sprintf("ppt/charts/chart%d.xml", i+1), fmt.Sprintf("ppt/embeddings/Microsoft_Excel_Sheet%d.xlsx", i+1)} {
			if keep[p] {
				return nil, fmt.Errorf("the template's layouts already contain %s, so charts cannot be added; use a template without charts on its layouts", p)
			}
		}
	}

	// Notes master: the template's own, or ours.
	presRels := parseRels(t.parts["ppt/_rels/presentation.xml.rels"], "ppt")
	notesTarget := ""
	for _, r := range presRels {
		if r.Type == relBase+"notesMaster" && keep[r.Target] {
			notesTarget = "../" + strings.TrimPrefix(r.Target, "ppt/")
		}
	}
	addNotesMaster := hasNotes && notesTarget == ""
	notesPart, notesTheme := "", ""
	if addNotesMaster {
		notesPart = freeName(keep, "ppt/notesMasters/notesMaster%d.xml")
		notesTheme = freeName(keep, "ppt/theme/theme%d.xml")
		notesTarget = "../" + strings.TrimPrefix(notesPart, "ppt/")
	}

	var files []pkgFile
	add := func(name, body string) { files = append(files, pkgFile{name, []byte(body)}) }

	// [Content_Types].xml first, as Office writes it.
	var ct strings.Builder
	ct.WriteString(xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	var ctDoc struct {
		Defaults []struct {
			Ext string `xml:"Extension,attr"`
			CT  string `xml:"ContentType,attr"`
		} `xml:"Default"`
		Overrides []struct {
			Part string `xml:"PartName,attr"`
			CT   string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	_ = xml.Unmarshal(t.parts["[Content_Types].xml"], &ctDoc)
	have := map[string]bool{}
	def := func(ext, typ string) {
		if !have[strings.ToLower(ext)] {
			have[strings.ToLower(ext)] = true
			ct.WriteString(`<Default Extension="` + escapeXML(ext) + `" ContentType="` + escapeXML(typ) + `"/>`)
		}
	}
	for _, x := range ctDoc.Defaults {
		def(x.Ext, x.CT)
	}
	def("rels", "application/vnd.openxmlformats-package.relationships+xml")
	def("xml", "application/xml")
	if len(d.charts) > 0 {
		def("xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	}
	for _, im := range d.images {
		switch {
		case strings.HasSuffix(im.name, ".png"):
			def("png", "image/png")
		case strings.HasSuffix(im.name, ".jpg"):
			def("jpg", "image/jpeg")
		}
	}
	overridden := map[string]bool{}
	over := func(part, typ string) {
		if !overridden[part] {
			overridden[part] = true
			ct.WriteString(`<Override PartName="/` + escapeXML(part) + `" ContentType="` + escapeXML(typ) + `"/>`)
		}
	}
	over("ppt/presentation.xml", ctPresentation)
	for _, x := range ctDoc.Overrides {
		part := strings.TrimPrefix(x.Part, "/")
		if keep[part] && !tplRegenerated[part] {
			over(part, x.CT)
		}
	}
	for i := range d.slides {
		over(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), pmlPrefix+"slide+xml")
		if strings.TrimSpace(d.slides[i].notes) != "" {
			over(fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", i+1), pmlPrefix+"notesSlide+xml")
		}
	}
	for i := range d.charts {
		over(fmt.Sprintf("ppt/charts/chart%d.xml", i+1), "application/vnd.openxmlformats-officedocument.drawingml.chart+xml")
	}
	if addNotesMaster {
		over(notesPart, pmlPrefix+"notesMaster+xml")
		over(notesTheme, "application/vnd.openxmlformats-officedocument.theme+xml")
	}
	over("docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml")
	over("docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml")
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

	// Presentation part and its relationships: the template's, minus its slides, plus ours.
	var rels strings.Builder
	rels.WriteString(relsHead)
	maxID := 0
	for _, r := range presRels {
		if r.Type == relBase+"slide" {
			continue
		}
		if r.Mode != "External" && !keep[r.Target] {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(r.ID, "rId")); err == nil {
			maxID = max(maxID, n)
		}
		mode := ""
		if r.Mode != "" {
			mode = ` TargetMode="` + escapeXML(r.Mode) + `"`
		}
		rels.WriteString(`<Relationship Id="` + escapeXML(r.ID) + `" Type="` + escapeXML(r.Type) + `" Target="` + escapeXML(r.Raw) + `"` + mode + `/>`)
	}
	next := maxID + 1
	notesRID := ""
	if addNotesMaster {
		notesRID = fmt.Sprintf("rId%d", next)
		next++
		rels.WriteString(relXML(notesRID, relBase+"notesMaster", strings.TrimPrefix(notesPart, "ppt/")))
	}
	slideRIDs := make([]string, len(d.slides))
	for i := range d.slides {
		slideRIDs[i] = fmt.Sprintf("rId%d", next)
		next++
		rels.WriteString(relXML(slideRIDs[i], relBase+"slide", fmt.Sprintf("slides/slide%d.xml", i+1)))
	}
	rels.WriteString(`</Relationships>`)
	pres, err := rewritePresentation(string(t.parts["ppt/presentation.xml"]), slideRIDs, notesRID)
	if err != nil {
		return nil, err
	}
	add("ppt/presentation.xml", pres)
	add("ppt/_rels/presentation.xml.rels", rels.String())

	// The template's masters, layouts, themes and media, untouched.
	for _, name := range t.order {
		if keep[name] && !tplRegenerated[name] {
			files = append(files, pkgFile{name, t.parts[name]})
		}
	}
	if addNotesMaster {
		add(notesPart, notesMasterXML())
		add(relsFileFor(notesPart), relsHead+relXML("rId1", relBase+"theme", "../"+strings.TrimPrefix(notesTheme, "ppt/"))+`</Relationships>`)
		add(notesTheme, d.themeXML("AgentGo Notes"))
	}
	files = append(files, d.contentParts(notesTarget)...)
	return zipFiles(files, now)
}

// freeName returns the first name from the pattern (one %d) not in use.
func freeName(used map[string]bool, pattern string) string {
	for n := 1; ; n++ {
		if name := fmt.Sprintf(pattern, n); !used[name] {
			return name
		}
	}
}
