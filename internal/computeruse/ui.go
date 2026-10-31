package computeruse

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// This file is the platform-neutral half of the accessibility ("UI
// Automation") channel. Pixel clicking is the universal fallback; where an app
// exposes an accessibility tree the model can instead address controls by name
// or id, which survives DPI, theme, language-pack and layout changes that make
// coordinate clicks drift.

// UIRect is an element's bounding box in physical screen pixels.
type UIRect struct{ X, Y, W, H int }

// Empty reports whether the box has no area (off-screen or collapsed).
func (r UIRect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Center returns the middle of the box.
func (r UIRect) Center() (float64, float64) {
	return float64(r.X) + float64(r.W)/2, float64(r.Y) + float64(r.H)/2
}

// UINode is one element of a window's accessibility tree digest.
type UINode struct {
	ID           int // 0-based traversal index; stable only for the same window + max_depth
	Depth        int
	Role         string // Button, Edit, ListItem ...
	Name         string
	AutomationID string
	Class        string
	Help         string
	Enabled      bool
	Rect         UIRect
}

// UITarget picks the window. Empty Window means the foreground window.
type UITarget struct {
	Window string // case-insensitive substring of the window title
}

// UISelector picks one element. ID wins when set; otherwise every non-empty
// field must match. An exact-name pass is tried before the substring pass.
type UISelector struct {
	ID           *int
	Role         string
	Name         string // substring
	NameExact    string
	AutomationID string // exact, or a dotted-path suffix
}

// Empty reports whether nothing was specified to select by.
func (s UISelector) Empty() bool {
	return s.ID == nil && s.Role == "" && s.Name == "" && s.NameExact == "" && s.AutomationID == ""
}

// UIAction is what to do with the selected element.
type UIAction string

const (
	UIPress    UIAction = "press"     // Invoke / Toggle / Select / Expand / default action
	UISetValue UIAction = "set_value" // ValuePattern.SetValue
	UIRead     UIAction = "read"      // current value (or name)
	UIFocus    UIAction = "focus"
)

// ErrNoPattern means the element has no accessibility pattern for the action.
// For press the Controller then falls back to clicking the element's center.
var ErrNoPattern = errors.New("element supports no accessibility pattern for this action")

// Accessibility reads and drives an app through its accessibility tree.
// Implementations are used from one goroutine at a time (the Controller
// serializes) but must pin their own OS thread: COM apartments are per-thread.
type Accessibility interface {
	// Tree returns the resolved window title and a depth-limited digest.
	Tree(t UITarget, maxDepth int) (title string, nodes []UINode, err error)
	// Act finds the element and performs the action. The returned string is
	// the value for UIRead and a short note otherwise.
	Act(t UITarget, maxDepth int, sel UISelector, act UIAction, value string) (UINode, string, error)
}

const (
	defaultUIDepth = 8
	maxUIDepth     = 20
	maxUINodes     = 600
	maxUIChildren  = 200
	nameDigestMax  = 120
)

// matchNode reports whether n satisfies sel's criteria (ID is handled by the
// caller). exact selects the strict name pass.
func matchNode(sel UISelector, n UINode, exact bool) bool {
	if sel.Role != "" && !strings.EqualFold(n.Role, sel.Role) {
		return false
	}
	if sel.AutomationID != "" && n.AutomationID != sel.AutomationID && !strings.HasSuffix(n.AutomationID, sel.AutomationID) {
		return false
	}
	if sel.NameExact != "" && strings.TrimSpace(n.Name) != strings.TrimSpace(sel.NameExact) {
		return false
	}
	if sel.Name != "" {
		if exact {
			if strings.TrimSpace(n.Name) != strings.TrimSpace(sel.Name) {
				return false
			}
		} else if !strings.Contains(strings.ToLower(n.Name), strings.ToLower(sel.Name)) {
			return false
		}
	}
	return true
}

// needsTwoPasses is true when an exact-name pass can differ from the
// substring pass.
func (s UISelector) needsTwoPasses() bool { return s.ID == nil && s.Name != "" }

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > limit {
		r := []rune(s)
		s = string(r[:limit]) + "…"
	}
	return s
}

// toImage maps a screen pixel to the latest screenshot's pixels.
func (f frame) toImage(x, y float64) (float64, float64) {
	return x * float64(f.imgW) / float64(f.screenW), y * float64(f.imgH) / float64(f.screenH)
}

// formatNode renders one node. When f is non-nil the center is given in
// screenshot pixels, ready to pass to click.
func formatNode(n UINode, f *frame) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d [%s]", strings.Repeat("  ", min(n.Depth, 12)), n.ID, n.Role)
	if n.Name != "" {
		fmt.Fprintf(&b, " %q", oneLine(n.Name, nameDigestMax))
	}
	if n.AutomationID != "" {
		fmt.Fprintf(&b, " id=%s", oneLine(n.AutomationID, 60))
	}
	if !n.Enabled {
		b.WriteString(" disabled")
	}
	if !n.Rect.Empty() {
		cx, cy := n.Rect.Center()
		if f != nil {
			ix, iy := f.toImage(cx, cy)
			fmt.Fprintf(&b, " @(%.0f,%.0f)", ix, iy)
		} else {
			fmt.Fprintf(&b, " @screen(%.0f,%.0f)", cx, cy)
		}
	}
	return b.String()
}

func formatTree(title string, nodes []UINode, f *frame) string {
	var b strings.Builder
	fmt.Fprintf(&b, "UI tree of window %q: %d element(s).", title, len(nodes))
	if f != nil {
		b.WriteString(" @(x,y) is the element center in screenshot pixels.")
	} else {
		b.WriteString(" No screenshot yet, so @screen(x,y) is in physical screen pixels - take a screenshot before clicking by coordinate.")
	}
	b.WriteString(" Act with ui_press / ui_set_value / ui_read using element_id (the leading number) or role+name.\n")
	for _, n := range nodes {
		b.WriteString(formatNode(n, f))
		b.WriteByte('\n')
	}
	if len(nodes) >= maxUINodes {
		b.WriteString("(truncated: element cap reached - pass a smaller max_depth or target a narrower window)\n")
	}
	return b.String()
}
