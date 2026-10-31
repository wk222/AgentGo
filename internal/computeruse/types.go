package computeruse

import (
	"context"
	"image"
)

// Point is one model-space coordinate (pixels of the last screenshot, or of a
// zoom image when ZoomID is set).
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Request is one computer-use action. It is both the tool's input schema and
// the sidecar's RPC arguments, so there is exactly one vocabulary.
//
// NOTE: jsonschema descriptions must not contain commas (eino's tag parser
// splits on them).
type Request struct {
	Action string `json:"action" jsonschema:"description=What to do: screenshot or zoom or click or move or scroll or drag or type or key or wait or batch or ui_tree or ui_press or ui_set_value or ui_read or ui_focus. Start with screenshot. Prefer the ui_ actions when the app exposes an accessibility tree (check with ui_tree) because they ignore pixel layout."`

	Window       string `json:"window,omitempty" jsonschema:"description=ui_ actions: part of the target window title (case-insensitive). Empty means the foreground window."`
	ElementID    *int   `json:"element_id,omitempty" jsonschema:"description=ui_ actions: the number at the start of a ui_tree line. Only valid with the same window and max_depth as that ui_tree."`
	Role         string `json:"role,omitempty" jsonschema:"description=ui_ actions: control role to match such as Button or Edit or ListItem or MenuItem or CheckBox."`
	Name         string `json:"name,omitempty" jsonschema:"description=ui_ actions: part of the element label (case-insensitive). An exact label match is preferred."`
	NameExact    string `json:"name_exact,omitempty" jsonschema:"description=ui_ actions: the element label must equal this."`
	AutomationID string `json:"automation_id,omitempty" jsonschema:"description=ui_ actions: the element automation id (or the tail of a dotted id path)."`
	MaxDepth     int    `json:"max_depth,omitempty" jsonschema:"description=ui_ actions: how deep to walk the tree (default 8 and max 20)."`
	Value        string `json:"value,omitempty" jsonschema:"description=ui_set_value: the text to put in the field."`

	X *float64 `json:"x,omitempty" jsonschema:"description=Horizontal pixel in the most recent screenshot (or zoom image when zoom_id is set). Origin is top-left."`
	Y *float64 `json:"y,omitempty" jsonschema:"description=Vertical pixel in the most recent screenshot (or zoom image when zoom_id is set). Origin is top-left."`

	X1 *float64 `json:"x1,omitempty" jsonschema:"description=zoom: top-left corner x of the region in screenshot pixels."`
	Y1 *float64 `json:"y1,omitempty" jsonschema:"description=zoom: top-left corner y of the region in screenshot pixels."`
	X2 *float64 `json:"x2,omitempty" jsonschema:"description=zoom: bottom-right corner x of the region in screenshot pixels."`
	Y2 *float64 `json:"y2,omitempty" jsonschema:"description=zoom: bottom-right corner y of the region in screenshot pixels."`

	ZoomID string `json:"zoom_id,omitempty" jsonschema:"description=Interpret x and y as pixels of this zoom image instead of the full screenshot."`

	Button string `json:"button,omitempty" jsonschema:"description=click: left (default) or right."`
	Clicks int    `json:"clicks,omitempty" jsonschema:"description=click: 1 (default) or 2 for a double click."`

	DX float64 `json:"dx,omitempty" jsonschema:"description=scroll: horizontal lines (positive scrolls right)."`
	DY float64 `json:"dy,omitempty" jsonschema:"description=scroll: vertical lines (positive scrolls down)."`

	Path []Point `json:"path,omitempty" jsonschema:"description=drag: at least two points. The button is held from the first to the last."`

	Text string `json:"text,omitempty" jsonschema:"description=type: literal text to enter (any Unicode). Use key for shortcuts."`
	Keys string `json:"keys,omitempty" jsonschema:"description=key: a key or chord such as enter or ctrl+c or alt+tab or win+d. Exactly one non-modifier key."`

	Ms int `json:"ms,omitempty" jsonschema:"description=wait: milliseconds to pause (max 10000)."`

	Steps            []Step `json:"steps,omitempty" jsonschema:"description=batch: actions run in order with no screenshot in between. Coordinates all refer to the screenshot you already have."`
	AllowDestructive bool   `json:"allow_destructive,omitempty" jsonschema:"description=batch: set true to confirm a step presses a key that deletes or cuts or closes or locks."`
}

// Step is one action inside a batch. It is a separate type from Request on
// purpose: Request.Steps []Request would be a recursive type, and the tool
// schema generator expands recursion without bound (stack overflow).
type Step struct {
	Action string   `json:"action" jsonschema:"description=click or move or scroll or drag or type or key or wait."`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	ZoomID string   `json:"zoom_id,omitempty"`
	Button string   `json:"button,omitempty"`
	Clicks int      `json:"clicks,omitempty"`
	DX     float64  `json:"dx,omitempty"`
	DY     float64  `json:"dy,omitempty"`
	Path   []Point  `json:"path,omitempty"`
	Text   string   `json:"text,omitempty"`
	Keys   string   `json:"keys,omitempty"`
	Ms     int      `json:"ms,omitempty"`
}

func (s Step) request() Request {
	return Request{
		Action: s.Action, X: s.X, Y: s.Y, ZoomID: s.ZoomID, Button: s.Button, Clicks: s.Clicks,
		DX: s.DX, DY: s.DY, Path: s.Path, Text: s.Text, Keys: s.Keys, Ms: s.Ms,
	}
}

// Result is a successful action: a one-line summary and, for most actions,
// the screen afterwards as PNG.
type Result struct {
	Text   string `json:"text"`
	PNG    []byte `json:"png,omitempty"`
	ZoomID string `json:"zoomId,omitempty"`
}

// Doer is anything that can execute a Request: the in-process Controller or a
// Client talking to the sidecar over a named pipe.
type Doer interface {
	Do(ctx context.Context, req Request) (*Result, error)
}

// Substrate is the OS layer. Coordinates are physical pixels of the primary
// display, origin top-left. Implementations must be safe to call from one
// goroutine at a time (the Controller serializes).
type Substrate interface {
	ScreenSize() (w, h int, err error)
	Capture() (*image.RGBA, error)
	// Preflight refuses input the OS would silently drop or that must not be
	// sent: the secure desktop is up (UAC) or the point lies over a window of a
	// higher integrity level (UIPI). hasPoint is false for key/type.
	Preflight(hasPoint bool, x, y float64) *Error
	MoveTo(x, y float64) error
	Click(button string, x, y float64, clicks int) error
	Scroll(dx, dy float64) error
	Drag(path []Point) error
	TypeText(s string) error
	Press(keys string) error
}
