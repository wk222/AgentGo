package browsercontrol

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// snapshot is what snapshotScript returns.
type snapshot struct {
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	ScrollY       int           `json:"scrollY"`
	ScrollHeight  int           `json:"scrollHeight"`
	ViewHeight    int           `json:"viewHeight"`
	Elements      []snapElement `json:"elements"`
	TotalElements int           `json:"totalElements"`
	Frames        int           `json:"frames"`
	Text          string        `json:"text"`
	TextTotal     int           `json:"textTotal"`
}

type snapElement struct {
	Ref         int      `json:"ref"`
	Role        string   `json:"role"`
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Href        string   `json:"href,omitempty"`
	Value       string   `json:"value,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	Options     []string `json:"options,omitempty"`
	OptionCount int      `json:"optionCount,omitempty"`
	Checked     *bool    `json:"checked,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
	ReadOnly    bool     `json:"readonly,omitempty"`
	Focused     bool     `json:"focused,omitempty"`
	Expanded    *bool    `json:"expanded,omitempty"`
	InView      bool     `json:"inView"`
}

func parseSnapshot(raw string) (snapshot, error) {
	var s snapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("the page returned an unreadable snapshot: %w", err)
	}
	return s, nil
}

// render turns a snapshot into the plain text the model reads. Values of
// password fields are never included, only the fact that the field is one.
func (s snapshot) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\nURL: %s\n", oneLine(s.Title), s.URL)
	if s.ScrollHeight > s.ViewHeight && s.ViewHeight > 0 {
		fmt.Fprintf(&b, "Scroll: %d of %d px (window %d px tall)\n", s.ScrollY, s.ScrollHeight, s.ViewHeight)
	}
	if s.Frames > 0 {
		fmt.Fprintf(&b, "Frames on the page: %d (same-site frames are included below)\n", s.Frames)
	}
	if len(s.Elements) == 0 {
		b.WriteString("\nNo clickable or fillable elements are visible.\n")
	} else {
		b.WriteString("\nElements (use the number in [ ] as ref; refs are only valid until the next snapshot):\n")
		for _, e := range s.Elements {
			b.WriteString(e.line())
			b.WriteByte('\n')
		}
		if s.TotalElements > len(s.Elements) {
			fmt.Fprintf(&b, "(%d of %d elements shown; scroll to bring others into view)\n", len(s.Elements), s.TotalElements)
		}
	}
	if strings.TrimSpace(s.Text) != "" {
		b.WriteString("\nPage text:\n")
		b.WriteString(s.Text)
		if s.TextTotal > len([]rune(s.Text)) {
			fmt.Fprintf(&b, "\n(text cut at %d of %d characters)", len([]rune(s.Text)), s.TextTotal)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (e snapElement) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d] %s", e.Ref, e.Role)
	if e.Type != "" && e.Type != "text" && e.Role == "textbox" {
		fmt.Fprintf(&b, "(%s)", e.Type)
	}
	if e.Name != "" {
		fmt.Fprintf(&b, " %q", e.Name)
	}
	switch {
	case e.Secret:
		b.WriteString(" [password field: the person types this, never the agent]")
	case e.Role == "combobox" && len(e.Options) > 0:
		more := ""
		if e.OptionCount > len(e.Options) {
			more = fmt.Sprintf(" | … (%d options)", e.OptionCount)
		}
		fmt.Fprintf(&b, " value=%q options: %s%s", e.Value, strings.Join(e.Options, " | "), more)
	case e.Value != "":
		fmt.Fprintf(&b, " value=%q", e.Value)
	}
	if e.Href != "" {
		fmt.Fprintf(&b, " -> %s", e.Href)
	}
	if e.Checked != nil {
		if *e.Checked {
			b.WriteString(" [checked]")
		} else {
			b.WriteString(" [unchecked]")
		}
	}
	if e.Expanded != nil {
		if *e.Expanded {
			b.WriteString(" [expanded]")
		} else {
			b.WriteString(" [collapsed]")
		}
	}
	if e.Disabled {
		b.WriteString(" [disabled]")
	}
	if e.ReadOnly {
		b.WriteString(" [read-only]")
	}
	if e.Focused {
		b.WriteString(" [focused]")
	}
	if !e.InView {
		b.WriteString(" [off screen]")
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// sameName reports whether the name the agent gave for an element agrees with
// the name the page gives it now. It is what stops a click aimed at "Save" from
// landing on whatever now sits at that number after the page changed. Elements
// without a name on the page cannot be checked and always agree.
func sameName(asked, actual string) bool {
	a, b := foldName(asked), foldName(actual)
	if b == "" {
		return true
	}
	if a == "" {
		return false
	}
	if a == b {
		return true
	}
	// One may be a shortened form of the other (the page name is cut at 80
	// characters; the agent may drop a trailing counter). Containment counts on
	// whole words, so "a" never matches "Delete"; text without spaces (Chinese)
	// has no words, so there two or more characters must be contained.
	if strings.Contains(" "+a+" ", " "+b+" ") || strings.Contains(" "+b+" ", " "+a+" ") {
		return true
	}
	short, long := a, b
	if len([]rune(short)) > len([]rune(long)) {
		short, long = long, short
	}
	return hasWide(short) && len([]rune(short)) >= 2 && strings.Contains(long, short)
}

func hasWide(s string) bool {
	for _, r := range s {
		if r >= 0x2E80 {
			return true
		}
	}
	return false
}

func foldName(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsSpace(r):
			space = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}
