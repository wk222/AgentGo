package computeruse

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxWaitMs   = 10000
	maxBatch    = 25
	maxZooms    = 8
	maxDragPath = 64
	// stopHold is how long a user Stop keeps refusing input. Every refused call
	// renews it, so a model that keeps hammering stays blocked and only a
	// genuinely new turn (quiet for stopHold) resumes.
	stopHold = 20 * time.Second
)

// Controller runs actions against a Substrate. One action runs at a time; a
// concurrent call fails with session_busy instead of interleaving input.
type Controller struct {
	sub    Substrate
	ui     Accessibility // nil when the OS has no accessibility channel
	settle time.Duration
	now    func() time.Time

	mu      sync.Mutex // held for the duration of one action
	frame   *frame
	zooms   map[string]zoom
	zoomSeq int

	stopMu       sync.Mutex
	blockedUntil time.Time
	stopped      atomic.Bool
}

// Option tunes a Controller.
type Option func(*Controller)

// WithSettle sets the pause between an input action and the follow-up
// screenshot, so the UI has time to react. Default 250ms.
func WithSettle(d time.Duration) Option { return func(c *Controller) { c.settle = d } }

// WithAccessibility enables the ui_* actions. A nil value leaves them off.
func WithAccessibility(a Accessibility) Option { return func(c *Controller) { c.ui = a } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(c *Controller) { c.now = now } }

// New returns a Controller over sub.
func New(sub Substrate, opts ...Option) *Controller {
	c := &Controller{sub: sub, settle: 250 * time.Millisecond, now: time.Now, zooms: map[string]zoom{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Stop is the user's brake: input is refused until the hold lapses. It is not
// reachable from the model - only from the hotkey or UI.
func (c *Controller) Stop() {
	c.stopMu.Lock()
	c.blockedUntil = c.now().Add(stopHold)
	c.stopMu.Unlock()
	c.stopped.Store(true)
}

// Resume clears a Stop immediately (UI "continue" button).
func (c *Controller) Resume() {
	c.stopMu.Lock()
	c.blockedUntil = time.Time{}
	c.stopMu.Unlock()
	c.stopped.Store(false)
}

func (c *Controller) refusedByStop() *Error {
	if !c.stopped.Load() {
		return nil
	}
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	if !c.now().Before(c.blockedUntil) {
		c.stopped.Store(false)
		return nil
	}
	c.blockedUntil = c.now().Add(stopHold) // renew: refusing is activity
	return NewError(CodeUserAborted,
		"The person at the machine pressed Stop, which released control; no input is sent until a new request after a pause.")
}

// Do executes one action. Failures are always *Error.
func (c *Controller) Do(ctx context.Context, req Request) (*Result, error) {
	if e := c.refusedByStop(); e != nil {
		return nil, e
	}
	if !c.mu.TryLock() {
		return nil, NewError(CodeSessionBusy, "another computer_use action is still running; the user decides which one continues")
	}
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, NewError(CodeTimeout, "the deadline passed before the tool ran, so nothing was actuated; calling again is safe")
	}

	act := strings.ToLower(strings.TrimSpace(req.Action))
	switch act {
	case "screenshot":
		return c.screenshot("")
	case "zoom":
		return c.zoomAction(req)
	case "wait":
		return c.waitAction(ctx, req)
	case "ui_tree", "ui_press", "ui_set_value", "ui_read", "ui_focus":
		return c.uiAction(ctx, act, req)
	case "":
		return nil, NewError(CodeInvalidArguments, "action is required (screenshot, zoom, click, move, scroll, drag, type, key, wait, batch, ui_tree, ui_press, ui_set_value, ui_read, ui_focus)")
	}

	if act == "batch" {
		return c.batch(ctx, req)
	}
	text, e := c.step(ctx, act, req)
	if e != nil {
		return nil, e
	}
	return c.afterInput(ctx, text)
}

// afterInput lets the UI settle, then returns the screen as the result.
func (c *Controller) afterInput(ctx context.Context, text string) (*Result, error) {
	if c.settle > 0 {
		select {
		case <-time.After(c.settle):
		case <-ctx.Done():
		}
	}
	res, err := c.screenshot("")
	if err != nil {
		// The action landed; only the follow-up capture failed.
		e := AsError(err)
		return &Result{Text: text + " (the screenshot afterwards failed: " + e.Message + " - take one before acting again)"}, nil
	}
	res.Text = text + "\n" + res.Text
	return res, nil
}

// screenshot captures the screen, rebuilds the frame and drops stale zooms.
// note is appended to the result text.
func (c *Controller) screenshot(note string) (*Result, error) {
	full, err := c.sub.Capture()
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "failed to capture a display screenshot: %v", err)
	}
	f := newFrame(full.Bounds().Dx(), full.Bounds().Dy())
	data, err := renderFrame(full, f)
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "%v", err)
	}
	c.frame = &f
	c.zooms = map[string]zoom{}
	text := fmt.Sprintf("Screenshot %dx%d pixels (origin top-left); the display is %dx%d. Pass coordinates in this image's pixels.", f.imgW, f.imgH, f.screenW, f.screenH)
	if note != "" {
		text = note + "\n" + text
	}
	return &Result{Text: text, PNG: data}, nil
}

func (c *Controller) needFrame() (*frame, *Error) {
	if c.frame == nil {
		return nil, NewError(CodeScreenshotRequired, "no screenshot has been taken yet, so there are no coordinates to act on")
	}
	w, h, err := c.sub.ScreenSize()
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "reading the display size: %v", err)
	}
	if !c.frame.sameScreen(w, h) {
		c.frame, c.zooms = nil, map[string]zoom{}
		return nil, NewError(CodeScreenshotRequired, "the display changed size since the last screenshot (%dx%d now), so its coordinates no longer apply", w, h)
	}
	return c.frame, nil
}

func (c *Controller) zoomAction(req Request) (*Result, error) {
	f, e := c.needFrame()
	if e != nil {
		return nil, e
	}
	if req.X1 == nil || req.Y1 == nil || req.X2 == nil || req.Y2 == nil {
		return nil, NewError(CodeInvalidArguments, "x1, y1, x2 and y2 are required: the top-left and bottom-right corners of the region in pixels of the latest screenshot")
	}
	rect, e := f.rectToScreen(*req.X1, *req.Y1, *req.X2, *req.Y2)
	if e != nil {
		return nil, e
	}
	full, err := c.sub.Capture()
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "failed to capture the display for zoom: %v", err)
	}
	if full.Bounds().Dx() != f.screenW || full.Bounds().Dy() != f.screenH {
		c.frame = nil
		return nil, NewError(CodeScreenshotRequired, "the display changed size; take a new screenshot")
	}
	data, w, h, err := renderZoom(full, rect)
	if err != nil {
		return nil, NewError(CodeCaptureFailed, "failed to render the zoom image: %v", err)
	}
	c.zoomSeq++
	id := fmt.Sprintf("zoom-%d", c.zoomSeq)
	if len(c.zooms) >= maxZooms {
		c.zooms = map[string]zoom{} // simple eviction: the model re-zooms if it needs an old one
	}
	c.zooms[id] = zoom{rect: rect, imgW: w, imgH: h}
	return &Result{
		ZoomID: id, PNG: data,
		Text: fmt.Sprintf("Zoomed into screenshot region (%.0f,%.0f)-(%.0f,%.0f); image is %dx%d. To act on a target located in THIS image pass zoom_id %q with x and y in this image's pixels. Every action still returns a full screenshot.",
			*req.X1, *req.Y1, *req.X2, *req.Y2, w, h, id),
	}, nil
}

func (c *Controller) waitAction(ctx context.Context, req Request) (*Result, error) {
	if req.Ms <= 0 || req.Ms > maxWaitMs {
		return nil, NewError(CodeInvalidArguments, "ms must be between 1 and %d", maxWaitMs)
	}
	select {
	case <-time.After(time.Duration(req.Ms) * time.Millisecond):
	case <-ctx.Done():
		return nil, NewError(CodeTimeout, "the deadline passed while waiting")
	}
	return c.screenshot(fmt.Sprintf("Waited %d ms.", req.Ms))
}

// point resolves the request's x,y (screenshot or zoom pixels) to the screen.
func (c *Controller) point(req Request, x, y *float64) (float64, float64, *Error) {
	if x == nil || y == nil {
		return 0, 0, NewError(CodeInvalidArguments, "x and y are required")
	}
	f, e := c.needFrame()
	if e != nil {
		return 0, 0, e
	}
	if req.ZoomID != "" {
		z, ok := c.zooms[req.ZoomID]
		if !ok {
			return 0, 0, NewError(CodeScreenshotRequired,
				"zoom_id %q is no longer held or no longer matches the display; take a new zoom of the region, then repeat the action with its zoom_id", req.ZoomID)
		}
		return z.toScreen(*x, *y)
	}
	return f.toScreen(*x, *y)
}

// step performs one input action without taking a screenshot.
func (c *Controller) step(ctx context.Context, act string, req Request) (string, *Error) {
	switch act {
	case "click", "double_click", "right_click":
		button, clicks := strings.ToLower(req.Button), req.Clicks
		if button == "" {
			button = "left"
		}
		if clicks == 0 {
			clicks = 1
		}
		if act == "double_click" {
			clicks = 2
		}
		if act == "right_click" {
			button = "right"
		}
		if button != "left" && button != "right" {
			return "", NewError(CodeInvalidArguments, "button must be left or right, got %q", req.Button)
		}
		if clicks < 1 || clicks > 2 {
			return "", NewError(CodeInvalidArguments, "clicks must be 1 or 2")
		}
		sx, sy, e := c.point(req, req.X, req.Y)
		if e != nil {
			return "", e
		}
		if e := c.sub.Preflight(true, sx, sy); e != nil {
			return "", e
		}
		if err := c.sub.Click(button, sx, sy, clicks); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Clicked %s x%d at screen (%.0f, %.0f).", button, clicks, sx, sy), nil

	case "move":
		sx, sy, e := c.point(req, req.X, req.Y)
		if e != nil {
			return "", e
		}
		if e := c.sub.Preflight(true, sx, sy); e != nil {
			return "", e
		}
		if err := c.sub.MoveTo(sx, sy); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Moved the pointer to screen (%.0f, %.0f).", sx, sy), nil

	case "scroll":
		if req.DX == 0 && req.DY == 0 {
			return "", NewError(CodeInvalidArguments, "scroll needs a non-zero dx or dy (lines)")
		}
		if req.X != nil && req.Y != nil { // scroll where the cursor is pointed
			sx, sy, e := c.point(req, req.X, req.Y)
			if e != nil {
				return "", e
			}
			if e := c.sub.Preflight(true, sx, sy); e != nil {
				return "", e
			}
			if err := c.sub.MoveTo(sx, sy); err != nil {
				return "", AsError(err)
			}
		} else if e := c.sub.Preflight(false, 0, 0); e != nil {
			return "", e
		}
		if err := c.sub.Scroll(req.DX, req.DY); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Scrolled dx=%.0f dy=%.0f lines.", req.DX, req.DY), nil

	case "drag":
		if len(req.Path) < 2 {
			return "", NewError(CodeInvalidArguments, "path must be an array of at least two {x,y} points")
		}
		if len(req.Path) > maxDragPath {
			return "", NewError(CodeInvalidArguments, "path has %d points; a drag path may have at most %d", len(req.Path), maxDragPath)
		}
		screen := make([]Point, len(req.Path))
		for i, p := range req.Path {
			x, y := p.X, p.Y
			sx, sy, e := c.point(req, &x, &y)
			if e != nil {
				return "", NewError(e.Code, "path[%d]: %s", i, e.Message)
			}
			screen[i] = Point{sx, sy}
		}
		if e := c.sub.Preflight(true, screen[0].X, screen[0].Y); e != nil {
			return "", e
		}
		if err := c.sub.Drag(screen); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Dragged a %d-point path from screen (%.0f, %.0f) to (%.0f, %.0f).",
			len(screen), screen[0].X, screen[0].Y, screen[len(screen)-1].X, screen[len(screen)-1].Y), nil

	case "type":
		if req.Text == "" {
			return "", NewError(CodeInvalidArguments, "text is required")
		}
		if bad := invisibleControl(req.Text); bad != 0 {
			return "", NewError(CodeInvalidArguments,
				"text contains control character U+%04X; injecting it would insert invisible characters or fire accelerators rather than type anything. Use action key if a key press was intended", bad)
		}
		if e := c.sub.Preflight(false, 0, 0); e != nil {
			return "", e
		}
		if err := c.sub.TypeText(req.Text); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Typed %d characters.", len([]rune(req.Text))), nil

	case "key":
		if _, _, err := parseCombo(req.Keys); err != nil {
			return "", NewError(CodeInvalidArguments, "%v", err)
		}
		if e := c.sub.Preflight(false, 0, 0); e != nil {
			return "", e
		}
		if err := c.sub.Press(req.Keys); err != nil {
			return "", AsError(err)
		}
		return fmt.Sprintf("Pressed %s.", req.Keys), nil

	case "wait":
		if req.Ms <= 0 || req.Ms > maxWaitMs {
			return "", NewError(CodeInvalidArguments, "ms must be between 1 and %d", maxWaitMs)
		}
		select {
		case <-time.After(time.Duration(req.Ms) * time.Millisecond):
		case <-ctx.Done():
			return "", NewError(CodeTimeout, "the deadline passed while waiting")
		}
		return fmt.Sprintf("Waited %d ms.", req.Ms), nil
	}
	return "", NewError(CodeInvalidArguments, "unknown action %q (screenshot, zoom, click, move, scroll, drag, type, key, wait, batch)", act)
}

// batch runs steps with no screenshot in between and one at the end. Every
// step is validated up front so a refusal leaves the screen untouched.
func (c *Controller) batch(ctx context.Context, req Request) (*Result, error) {
	if n := len(req.Steps); n == 0 || n > maxBatch {
		return nil, NewError(CodeInvalidArguments, "steps must contain 1 to %d actions", maxBatch)
	}
	for i, st := range req.Steps {
		s := st.request()
		act := strings.ToLower(strings.TrimSpace(s.Action))
		switch act {
		case "click", "double_click", "right_click", "move", "scroll", "drag", "type", "key", "wait":
		default:
			return nil, NewError(CodeInvalidArguments, "steps[%d]: %q cannot run inside a batch (screenshot and zoom need a look in between)", i, s.Action)
		}
		if act == "key" && !req.AllowDestructive && isDestructiveCombo(s.Keys) {
			return nil, NewError(CodeInvalidArguments,
				"steps[%d] presses %q, which deletes, cuts, closes or locks; a batch runs it without a look in between, so pass allow_destructive: true to confirm, or press it as a lone key action", i, s.Keys)
		}
	}
	var done []string
	for i, st := range req.Steps {
		s := st.request()
		if e := c.refusedByStop(); e != nil {
			return nil, partial(e, i, len(req.Steps))
		}
		if err := ctx.Err(); err != nil {
			return nil, NewError(CodeTimeout, "the deadline passed before step %d of %d; %d step(s) ran, take a screenshot to see where things stand", i+1, len(req.Steps), i)
		}
		text, e := c.step(ctx, strings.ToLower(strings.TrimSpace(s.Action)), s)
		if e != nil {
			return nil, partial(e, i, len(req.Steps))
		}
		done = append(done, fmt.Sprintf("%d. %s", i+1, text))
		if c.settle > 0 && i < len(req.Steps)-1 {
			time.Sleep(c.settle / 5)
		}
	}
	return c.afterInput(ctx, "Batch ran "+fmt.Sprint(len(req.Steps))+" steps:\n"+strings.Join(done, "\n"))
}

// partial annotates an error with how far a batch got, since earlier steps landed.
func partial(e *Error, failedIdx, total int) *Error {
	out := *e
	if failedIdx > 0 {
		out.Message = fmt.Sprintf("step %d of %d failed after %d step(s) already landed (take a screenshot before acting again): %s", failedIdx+1, total, failedIdx, e.Message)
	} else {
		out.Message = fmt.Sprintf("step 1 of %d failed, nothing was actuated: %s", total, e.Message)
	}
	return &out
}

// invisibleControl returns the first control rune that is neither newline nor
// tab (those are typed as Enter/Tab), or 0.
func invisibleControl(s string) rune {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return r
		}
		if r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return r
		}
	}
	return 0
}
