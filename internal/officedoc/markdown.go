package officedoc

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// AddMarkdown renders Markdown into d and returns the text of the first heading
// (useful as the document title). Models write Markdown fluently, so it is the
// authoring format for Word documents: headings, paragraphs, **bold** / *italic*
// / ~~strike~~ / `code`, links, nested bullet and numbered lists, GFM tables,
// fenced code, block quotes and --- rules. A line holding only
// <!-- pagebreak --> starts a new page.
func AddMarkdown(d *Doc, md string) (firstHeading string) {
	src := []byte(md)
	root := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough)).
		Parser().Parse(text.NewReader(src))
	c := &mdConv{d: d, src: src}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		c.block(n, 0)
	}
	return c.firstHeading
}

type mdConv struct {
	d            *Doc
	src          []byte
	firstHeading string
}

func (c *mdConv) block(n ast.Node, listLevel int) {
	switch v := n.(type) {
	case *ast.Heading:
		runs := c.inlines(v, Run{})
		if c.firstHeading == "" {
			c.firstHeading = plain(runs)
		}
		c.d.Heading(v.Level, runs)
	case *ast.Paragraph, *ast.TextBlock:
		c.d.Paragraph(c.inlines(v, Run{}))
	case *ast.List:
		c.list(v, listLevel)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		c.d.CodeBlock(c.codeLines(v))
	case *ast.Blockquote:
		for ch := v.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch ch.(type) {
			case *ast.Paragraph, *ast.TextBlock:
				c.d.Quote(c.inlines(ch, Run{}))
			default:
				c.block(ch, listLevel)
			}
		}
	case *ast.ThematicBreak:
		c.d.Rule()
	case *ast.HTMLBlock:
		if strings.Contains(strings.ToLower(c.rawLines(v)), "pagebreak") {
			c.d.PageBreak()
		}
	case *east.Table:
		c.table(v)
	}
}

func (c *mdConv) list(l *ast.List, level int) {
	numID := 0
	if l.IsOrdered() {
		numID = c.d.NewNumberedList()
	}
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		first := true
		hasText := false
		for ch := item.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch ch.(type) {
			case *ast.TextBlock, *ast.Paragraph:
				hasText = true
				runs := c.inlines(ch, Run{})
				if first {
					c.d.ListItem(numID, level, runs)
					first = false
				} else {
					c.d.Paragraph(runs) // a further paragraph inside the same item
				}
			case *ast.List:
				if first { // an item that opens with a nested list still shows its marker
					c.d.ListItem(numID, level, nil)
					first = false
				}
				c.list(ch.(*ast.List), level+1)
			default:
				c.block(ch, level+1)
			}
		}
		if !hasText && first {
			c.d.ListItem(numID, level, nil)
		}
	}
}

func (c *mdConv) table(t *east.Table) {
	var rows [][][]Run
	header := false
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		if _, ok := r.(*east.TableHeader); ok {
			header = true
		}
		var cells [][]Run
		for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, c.inlines(cell, Run{}))
		}
		rows = append(rows, cells)
	}
	c.d.Table(rows, header)
}

func (c *mdConv) codeLines(n ast.Node) []string {
	var lines []string
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		lines = append(lines, strings.TrimRight(string(seg.Value(c.src)), "\r\n"))
	}
	return lines
}

func (c *mdConv) rawLines(n ast.Node) string {
	return strings.Join(c.codeLines(n), "\n")
}

// inlines flattens the inline children of n into runs, carrying base styling.
func (c *mdConv) inlines(n ast.Node, base Run) []Run {
	var out []Run
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		switch v := ch.(type) {
		case *ast.Text:
			r := base
			r.Text = string(v.Segment.Value(c.src))
			out = append(out, r)
			switch {
			case v.HardLineBreak():
				out = append(out, Run{Break: true})
			case v.SoftLineBreak():
				sp := base
				sp.Text = " "
				out = append(out, sp)
			}
		case *ast.String:
			r := base
			r.Text = string(v.Value)
			out = append(out, r)
		case *ast.CodeSpan:
			r := base
			r.Code = true
			r.Text = plain(c.inlines(v, Run{}))
			out = append(out, r)
		case *ast.Emphasis:
			nb := base
			if v.Level >= 2 {
				nb.Bold = true
			} else {
				nb.Italic = true
			}
			out = append(out, c.inlines(v, nb)...)
		case *east.Strikethrough:
			nb := base
			nb.Strike = true
			out = append(out, c.inlines(v, nb)...)
		case *ast.Link:
			inner := c.inlines(v, base)
			out = append(out, inner...)
			if dest := string(v.Destination); dest != "" && dest != plain(inner) {
				r := base
				r.Text = " (" + dest + ")"
				out = append(out, r)
			}
		case *ast.AutoLink:
			r := base
			r.Text = string(v.URL(c.src))
			out = append(out, r)
		case *ast.Image:
			r := base
			r.Text = "[image: " + plain(c.inlines(v, Run{})) + "]"
			out = append(out, r)
		case *ast.RawHTML:
			// inline HTML has no Word equivalent here
		default:
			out = append(out, c.inlines(ch, base)...)
		}
	}
	return out
}

func plain(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		if !r.Break {
			b.WriteString(r.Text)
		}
	}
	return b.String()
}
