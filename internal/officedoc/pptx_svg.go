package officedoc

import (
	"errors"
	"strings"
)

// svgSlide builds a free-form slide from one constrained SVG document: the
// SVG is validated against a whitelist, flattened into a scene, and written
// out as native, editable DrawingML shapes (never as a picture).
//
// In template mode the slide sits on a layout of the template: the title is
// that layout's placeholder, and the svg draws the content.
func (d *deck) svgSlide(in pptxSlideIn) (*slide, error) {
	if strings.TrimSpace(in.Svg) == "" {
		return nil, errors.New("svg is required for the svg layout")
	}
	sc, err := d.convertSVG(in.Svg)
	if err != nil {
		return nil, err
	}
	for _, w := range sc.warnings {
		d.warn("%s", w)
	}
	var s *sb
	if d.tpl != nil {
		l := d.curTpl
		d.svgDropTitles(sc, l)
		d.svgOverlaps(sc, l)
		s = d.newSlide(l.N)
		if tp := l.titlePH(); tp != nil {
			d.checkTemplateTitle(in.Title, tp)
			s.phText(*tp, "Title", []string{in.Title}, nil)
		}
	} else {
		s = d.newSlide(layoutTitleOnly)
	}
	s.notes = in.Notes
	s.title = in.Title
	s.bgXML = sc.bg
	s.writeNodes(sc.nodes, true)
	return s.finish(), nil
}
