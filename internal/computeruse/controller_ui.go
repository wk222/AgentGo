package computeruse

import (
	"context"
	"errors"
	"fmt"
)

// uiAction runs the accessibility actions. They share the Controller's Stop,
// busy-lock and secure-desktop gates with pixel input, but need no prior
// screenshot: elements are addressed by id or by name, not by coordinate.
func (c *Controller) uiAction(ctx context.Context, act string, req Request) (*Result, error) {
	if c.ui == nil {
		return nil, NewError(CodeUnsupported, "this system has no accessibility channel; use screenshot with click, type and key instead")
	}
	target := UITarget{Window: req.Window}

	if act == "ui_tree" {
		title, nodes, err := c.ui.Tree(target, req.MaxDepth)
		if err != nil {
			return nil, AsError(err)
		}
		if len(nodes) <= 1 {
			return &Result{Text: fmt.Sprintf("Window %q exposes no accessibility tree (the app draws its own UI). Use screenshot with pixel actions for it.", title)}, nil
		}
		return &Result{Text: formatTree(title, nodes, c.frame)}, nil
	}

	sel := UISelector{ID: req.ElementID, Role: req.Role, Name: req.Name, NameExact: req.NameExact, AutomationID: req.AutomationID}
	if sel.Empty() {
		return nil, NewError(CodeInvalidArguments, "say which element: element_id from ui_tree, or role and/or name, name_exact, automation_id")
	}
	if e := c.sub.Preflight(false, 0, 0); e != nil {
		return nil, e
	}

	var kind UIAction
	switch act {
	case "ui_press":
		kind = UIPress
	case "ui_set_value":
		kind = UISetValue
	case "ui_read":
		kind = UIRead
	default:
		kind = UIFocus
	}

	node, note, err := c.ui.Act(target, req.MaxDepth, sel, kind, req.Value)
	if err != nil && kind == UIPress && errors.Is(err, ErrNoPattern) {
		return c.pressByPixel(ctx, node)
	}
	if err != nil {
		return nil, AsError(err)
	}

	what := fmt.Sprintf("%s [%s] %q (id %d)", act, node.Role, oneLine(node.Name, 60), node.ID)
	switch kind {
	case UIRead:
		return &Result{Text: fmt.Sprintf("%s value: %q", what, note)}, nil
	case UISetValue:
		return c.afterInput(ctx, fmt.Sprintf("%s: %s to %q.", what, note, oneLine(req.Value, 80)))
	}
	return c.afterInput(ctx, fmt.Sprintf("%s: %s.", what, note))
}

// pressByPixel is ui_press's fallback for an element with no actionable
// pattern (common in Qt and custom-drawn controls): click its center.
func (c *Controller) pressByPixel(ctx context.Context, n UINode) (*Result, error) {
	if n.Rect.Empty() {
		return nil, NewError(CodeInvalidArguments, "element %d (%q) has no actionable pattern and no on-screen area to click", n.ID, oneLine(n.Name, 60))
	}
	x, y := n.Rect.Center()
	w, h, err := c.sub.ScreenSize()
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "reading the display size: %v", err)
	}
	if x < 0 || y < 0 || x >= float64(w) || y >= float64(h) {
		return nil, NewError(CodeInvalidArguments, "element %d (%q) is off the display at (%.0f,%.0f); scroll it into view first", n.ID, oneLine(n.Name, 60), x, y)
	}
	if e := c.sub.Preflight(true, x, y); e != nil {
		return nil, e
	}
	if err := c.sub.Click("left", x, y, 1); err != nil {
		return nil, AsError(err)
	}
	return c.afterInput(ctx, fmt.Sprintf("ui_press [%s] %q (id %d): no accessibility pattern, so clicked its center at screen (%.0f, %.0f).",
		n.Role, oneLine(n.Name, 60), n.ID, x, y))
}
