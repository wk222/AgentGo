package officedoc

import (
	"archive/zip"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// Built-in templates: .pptx files compiled into AgentGo so that a machine with
// nothing but the exe can still build on a company look. create_pptx and
// inspect_pptx take "builtin:<name>" wherever they take a template path.
//
// The files live in builtin_tpl/ and are NOT part of the source tree (they
// carry a company's logo and art; see .gitignore and builtin_tpl/README.txt).
// A build without them has no built-in templates, and everything else works.
//
//go:embed builtin_tpl
var builtinEmbedded embed.FS

// builtinTemplates is where "builtin:<name>" is looked up. Tests replace it.
var builtinTemplates fs.FS = func() fs.FS {
	sub, err := fs.Sub(builtinEmbedded, "builtin_tpl")
	if err != nil {
		return builtinEmbedded
	}
	return sub
}()

const builtinPrefix = "builtin:"

var builtinNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)

func isBuiltinRef(ref string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref)), builtinPrefix)
}

// builtinNames lists the templates this build carries, sorted.
func builtinNames() []string {
	entries, err := fs.ReadDir(builtinTemplates, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		for _, ext := range []string{".pptx", ".potx"} {
			if !e.IsDir() && strings.HasSuffix(n, ext) {
				names = append(names, strings.TrimSuffix(n, ext))
			}
		}
	}
	sort.Strings(names)
	return names
}

// readBuiltin returns the file behind a "builtin:<name>" reference and the
// normalised name.
func readBuiltin(ref string) ([]byte, string, error) {
	name := strings.ToLower(strings.TrimSpace(strings.TrimSpace(ref)[len(builtinPrefix):]))
	for _, ext := range []string{".pptx", ".potx"} {
		name = strings.TrimSuffix(name, ext)
	}
	available := builtinNames()
	list := "none in this build, pass a template file from the workspace instead"
	if len(available) > 0 {
		list = strings.Join(available, ", ")
	}
	if !builtinNameRE.MatchString(name) {
		return nil, "", fmt.Errorf("%q is not a built-in template name (built-in: %s)", ref, list)
	}
	for _, ext := range []string{".pptx", ".potx"} {
		if data, err := fs.ReadFile(builtinTemplates, name+ext); err == nil {
			return data, name, nil
		}
	}
	return nil, "", fmt.Errorf("no built-in template named %q (built-in: %s)", name, list)
}

// openBuiltin reads a built-in template as a zip archive.
func openBuiltin(ref string) (*zip.Reader, string, error) {
	data, name, err := readBuiltin(ref)
	if err != nil {
		return nil, "", err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", fmt.Errorf("built-in template %s is damaged: %w", name, err)
	}
	return zr, name, nil
}

// --- making a built-in template from a real deck ---

// Parts that belong to the presentation's authors and history, not to its
// look; a built-in template never carries them.
var personalParts = []string{
	"ppt/commentAuthors", "ppt/comments/", "ppt/authors", "ppt/people", "ppt/revisionInfo",
	"ppt/changesInfos/", "customXml/", "docProps/custom",
}

// WPS and Office hang identifiers of the machine and the user on a deck as
// tags (for example COMMONDATA); a built-in template keeps the tag parts, since
// slides, layouts and the presentation point at them, but empties them.
const emptyTagList = xmlHeader + `<p:tagLst ` + nsA + ` ` + nsR + ` ` + nsP + `/>`

const blankSlideBody = `<p:cSld><p:spTree>` + spTreeHead + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`

// stripTemplate turns a company deck (or .potx) into a template without any
// of its content: masters, layouts, themes and the media they use are kept
// byte for byte; every slide is replaced by an empty one on the same layout
// (so the choice of cover and content layouts survives), and notes, charts,
// photos, comment authors, history and document properties are dropped.
func stripTemplate(src []byte, title string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("not a presentation: %w", err)
	}
	t, err := templateFromZip(zr.File, "source", "the source file")
	if err != nil {
		return nil, err
	}
	c := *t
	c.parts = map[string][]byte{}
	c.order = nil
	for _, name := range t.order {
		personal := false
		for _, p := range personalParts {
			personal = personal || strings.HasPrefix(name, p)
		}
		if personal {
			continue
		}
		c.parts[name] = t.parts[name]
		if strings.HasPrefix(name, "ppt/tags/") && strings.HasSuffix(name, ".xml") {
			c.parts[name] = []byte(emptyTagList)
		}
		c.order = append(c.order, name)
	}
	d := &deck{pal: palettes["blue"], font: defaultFont, numbers: true, fr: defaultFrame, tpl: &c, title: title}
	for _, n := range t.samples {
		if c.layouts[n] == nil {
			return nil, errors.New("a slide of the source file sits on a layout that does not exist")
		}
		d.slides = append(d.slides, &slide{xml: xmlHeader + `<p:sld ` + nsA + ` ` + nsR + ` ` + nsP + `>` + blankSlideBody + `</p:sld>`, layout: n})
	}
	return d.packTemplate(title, "")
}
