package tools

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var webWhitespace = regexp.MustCompile(`[\t\r\n ]+`)

// Keep source wording, links, headings and table row boundaries. Content is
// marked untrusted by the tool envelope; deleting suspicious phrases would
// change the evidence. This is text extraction, not a JavaScript browser.
func readableWebHTML(raw string, base *url.URL) (string, string, error) {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	var title string
	var main, article, body *html.Node
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if title == "" {
					title = strings.TrimSpace(webNodeText(n))
				}
			case "main":
				if main == nil {
					main = n
				}
			case "article":
				if article == nil {
					article = n
				}
			case "body":
				body = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			scan(c)
		}
	}
	scan(root)
	selected := body
	if main != nil {
		selected = main
	} else if article != nil {
		selected = article
	}
	if selected == nil {
		selected = root
	}
	var b strings.Builder
	var render func(*html.Node, bool)
	render = func(n *html.Node, pre bool) {
		if b.Len() > webBodyLimit {
			return
		}
		if n.Type == html.TextNode {
			if pre {
				b.WriteString(n.Data)
			} else {
				b.WriteString(webWhitespace.ReplaceAllString(n.Data, " "))
			}
			return
		}
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "hidden" || (a.Key == "aria-hidden" && a.Val == "true") {
					return
				}
			}
			switch n.Data {
			case "script", "style", "noscript", "template", "svg", "head", "nav", "footer", "aside", "form":
				return
			case "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteString("\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " ")
			case "p", "div", "section", "article", "main", "tr", "blockquote":
				b.WriteByte('\n')
			case "br":
				b.WriteByte('\n')
			case "li":
				b.WriteString("\n- ")
			case "td", "th":
				b.WriteByte('\t')
			case "pre":
				b.WriteString("\n```\n")
				pre = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			render(c, pre)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "a":
				for _, a := range n.Attr {
					if a.Key == "href" && len(a.Val) <= 4096 {
						if ref, err := url.Parse(a.Val); err == nil {
							link := base.ResolveReference(ref)
							if (link.Scheme == "http" || link.Scheme == "https") && link.User == nil {
								b.WriteString(" (" + link.String() + ")")
							}
						}
					}
				}
			case "pre":
				b.WriteString("\n```\n")
			case "p", "div", "section", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteByte('\n')
			}
		}
	}
	render(selected, false)
	if runes := []rune(title); len(runes) > 300 {
		title = string(runes[:300])
	}
	return title, strings.TrimSpace(b.String()), nil
}

func webNodeText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(webWhitespace.ReplaceAllString(n.Data, " "))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}
