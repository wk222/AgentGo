package computeruse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"
)

// fakeSub is a Substrate that records what the controller asked of it.
type fakeSub struct {
	mu        sync.Mutex
	w, h      int
	calls     []string
	preflight *Error
	clickErr  error
}

func newFake(w, h int) *fakeSub { return &fakeSub{w: w, h: h} }

func (f *fakeSub) rec(format string, a ...any) {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	f.mu.Unlock()
}
func (f *fakeSub) ScreenSize() (int, int, error) { return f.w, f.h, nil }
func (f *fakeSub) Capture() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, f.w, f.h))
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 0x80, 0xFF})
		}
	}
	return img, nil
}
func (f *fakeSub) Preflight(bool, float64, float64) *Error { return f.preflight }
func (f *fakeSub) MoveTo(x, y float64) error               { f.rec("move %.0f,%.0f", x, y); return nil }
func (f *fakeSub) Click(b string, x, y float64, n int) error {
	if f.clickErr != nil {
		return f.clickErr
	}
	f.rec("click %s %.0f,%.0f x%d", b, x, y, n)
	return nil
}
func (f *fakeSub) Scroll(dx, dy float64) error { f.rec("scroll %.0f,%.0f", dx, dy); return nil }
func (f *fakeSub) Drag(p []Point) error {
	f.rec("drag %d %.0f,%.0f>%.0f,%.0f", len(p), p[0].X, p[0].Y, p[len(p)-1].X, p[len(p)-1].Y)
	return nil
}
func (f *fakeSub) TypeText(s string) error { f.rec("type %q", s); return nil }
func (f *fakeSub) Press(k string) error    { f.rec("press %s", k); return nil }

func (f *fakeSub) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func fp(v float64) *float64 { return &v }

func codeOf(t *testing.T, err error) Code {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %T %v", err, err)
	}
	return e.Code
}

func pngSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("bad png: %v", err)
	}
	return cfg.Width, cfg.Height
}

func newCtl(sub Substrate) *Controller { return New(sub, WithSettle(0)) }

func TestFrameFitsCanvasWithoutUpscaling(t *testing.T) {
	cases := []struct{ sw, sh, iw, ih int }{
		{1920, 1080, 1456, 819},
		{5120, 2756, 1456, 784},
		{1280, 720, 1280, 720}, // smaller than the canvas: never upscaled
		{1000, 2000, 420, 840}, // portrait is bounded by height
	}
	for _, c := range cases {
		f := newFrame(c.sw, c.sh)
		if f.imgW != c.iw || f.imgH != c.ih {
			t.Errorf("%dx%d -> %dx%d, want %dx%d", c.sw, c.sh, f.imgW, f.imgH, c.iw, c.ih)
		}
	}
}

func TestClickRequiresScreenshotFirst(t *testing.T) {
	c := newCtl(newFake(1920, 1080))
	_, err := c.Do(context.Background(), Request{Action: "click", X: fp(10), Y: fp(10)})
	if codeOf(t, err) != CodeScreenshotRequired {
		t.Fatalf("got %v", err)
	}
	if e := AsError(err); e.Escalation != EscUseDifferentTool {
		t.Fatalf("escalation = %s", e.Escalation)
	}
}

func TestScreenshotThenClickMapsToScreenPixels(t *testing.T) {
	sub := newFake(1920, 1080)
	c := newCtl(sub)
	res, err := c.Do(context.Background(), Request{Action: "screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	if w, h := pngSize(t, res.PNG); w != 1456 || h != 819 {
		t.Fatalf("screenshot is %dx%d", w, h)
	}
	// Image centre must land on (about) the screen centre.
	res, err = c.Do(context.Background(), Request{Action: "click", X: fp(728), Y: fp(409)})
	if err != nil {
		t.Fatal(err)
	}
	if got := sub.last(); got != "click left 961,540 x1" && got != "click left 960,540 x1" {
		t.Fatalf("click landed at %q", got)
	}
	if len(res.PNG) == 0 {
		t.Fatal("an input action must return a fresh screenshot")
	}
}

func TestOutOfBoundsIsInvalidArguments(t *testing.T) {
	c := newCtl(newFake(1920, 1080))
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	_, err := c.Do(context.Background(), Request{Action: "click", X: fp(1456), Y: fp(5)})
	if codeOf(t, err) != CodeInvalidArguments {
		t.Fatalf("got %v", err)
	}
	_, err = c.Do(context.Background(), Request{Action: "click", X: fp(5)})
	if codeOf(t, err) != CodeInvalidArguments {
		t.Fatalf("missing y: got %v", err)
	}
}

func TestDisplayResizeInvalidatesCoordinates(t *testing.T) {
	sub := newFake(1920, 1080)
	c := newCtl(sub)
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	sub.w, sub.h = 2560, 1440
	_, err := c.Do(context.Background(), Request{Action: "click", X: fp(5), Y: fp(5)})
	if codeOf(t, err) != CodeScreenshotRequired {
		t.Fatalf("got %v", err)
	}
}

func TestZoomMapsToNativeRegionAndIsUsableForClicks(t *testing.T) {
	sub := newFake(1920, 1080)
	c := newCtl(sub)
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	// Image box (100,100)-(400,300) is screen (131.8..525.3, 131.8..394.4) at 1.3187x.
	z, err := c.Do(context.Background(), Request{Action: "zoom", X1: fp(100), Y1: fp(100), X2: fp(400), Y2: fp(300)})
	if err != nil {
		t.Fatal(err)
	}
	if z.ZoomID == "" {
		t.Fatal("zoom must return an id")
	}
	zw, zh := pngSize(t, z.PNG)
	if zw < 390 || zw > 400 || zh < 255 || zh > 270 { // native pixels, so sharper than the screenshot
		t.Fatalf("zoom image is %dx%d, want native ~395x263", zw, zh)
	}
	// Top-left pixel of the zoom image is the top-left of the screen region.
	if _, err = c.Do(context.Background(), Request{Action: "click", ZoomID: z.ZoomID, X: fp(0), Y: fp(0)}); err != nil {
		t.Fatal(err)
	}
	var x, y, n int
	var b string
	if _, scanErr := fmt.Sscanf(sub.last(), "click %s %d,%d x%d", &b, &x, &y, &n); scanErr != nil {
		t.Fatalf("parse %q: %v", sub.last(), scanErr)
	}
	if x < 131 || x > 134 || y < 131 || y > 134 {
		t.Fatalf("zoom (0,0) hit screen (%d,%d), want about (132,132)", x, y)
	}
	// A new screenshot invalidates every zoom id.
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	_, err = c.Do(context.Background(), Request{Action: "click", ZoomID: z.ZoomID, X: fp(1), Y: fp(1)})
	if codeOf(t, err) != CodeScreenshotRequired {
		t.Fatalf("stale zoom: got %v", err)
	}
}

func TestZoomRegionValidation(t *testing.T) {
	c := newCtl(newFake(1920, 1080))
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	for name, r := range map[string]Request{
		"outside":    {Action: "zoom", X1: fp(0), Y1: fp(0), X2: fp(5000), Y2: fp(100)},
		"too thin":   {Action: "zoom", X1: fp(10), Y1: fp(10), X2: fp(12), Y2: fp(200)},
		"missing x2": {Action: "zoom", X1: fp(10), Y1: fp(10), Y2: fp(50)},
	} {
		if _, err := c.Do(context.Background(), r); codeOf(t, err) != CodeInvalidArguments {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestStopBlocksInputAndRenewsWhileHammered(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	sub := newFake(1280, 720)
	c := New(sub, WithSettle(0), WithClock(clock))
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})

	c.Stop()
	for i := 0; i < 3; i++ { // the model keeps retrying: each refusal renews the hold
		now = now.Add(15 * time.Second)
		_, err := c.Do(context.Background(), Request{Action: "click", X: fp(5), Y: fp(5)})
		if codeOf(t, err) != CodeUserAborted {
			t.Fatalf("attempt %d: got %v", i, err)
		}
		if e := AsError(err); e.Escalation != EscStop {
			t.Fatalf("escalation = %s", e.Escalation)
		}
	}
	if len(sub.calls) != 0 {
		t.Fatalf("no input may be sent while stopped, got %v", sub.calls)
	}
	now = now.Add(stopHold + time.Second) // quiet for a full hold: a new turn
	if _, err := c.Do(context.Background(), Request{Action: "screenshot"}); err != nil {
		t.Fatalf("after the hold lapses: %v", err)
	}
	c.Stop()
	c.Resume()
	if _, err := c.Do(context.Background(), Request{Action: "click", X: fp(5), Y: fp(5)}); err != nil {
		t.Fatalf("after Resume: %v", err)
	}
}

func TestPreflightRefusalsPassThrough(t *testing.T) {
	sub := newFake(1280, 720)
	c := newCtl(sub)
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	sub.preflight = NewError(CodeSecureDesktop, "uac is up")
	_, err := c.Do(context.Background(), Request{Action: "type", Text: "hello"})
	if codeOf(t, err) != CodeSecureDesktop || len(sub.calls) != 0 {
		t.Fatalf("got %v calls=%v", err, sub.calls)
	}
}

func TestBatchRefusesDestructiveKeysUpFront(t *testing.T) {
	sub := newFake(1280, 720)
	c := newCtl(sub)
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	steps := []Step{
		{Action: "click", X: fp(10), Y: fp(10)},
		{Action: "key", Keys: "ctrl+w"},
	}
	_, err := c.Do(context.Background(), Request{Action: "batch", Steps: steps})
	if codeOf(t, err) != CodeInvalidArguments {
		t.Fatalf("got %v", err)
	}
	if len(sub.calls) != 0 {
		t.Fatalf("a refused batch must not run its first step: %v", sub.calls)
	}
	res, err := c.Do(context.Background(), Request{Action: "batch", Steps: steps, AllowDestructive: true})
	if err != nil || len(res.PNG) == 0 || len(sub.calls) != 2 {
		t.Fatalf("confirmed batch: err=%v calls=%v", err, sub.calls)
	}
}

func TestBatchStepFailureReportsProgress(t *testing.T) {
	sub := newFake(1280, 720)
	c := newCtl(sub)
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	_, err := c.Do(context.Background(), Request{Action: "batch", Steps: []Step{
		{Action: "type", Text: "ok"},
		{Action: "click", X: fp(99999), Y: fp(5)},
	}})
	e := AsError(err)
	if e == nil || e.Code != CodeInvalidArguments {
		t.Fatalf("got %v", err)
	}
	if want := "after 1 step(s) already landed"; !bytes.Contains([]byte(e.Message), []byte(want)) {
		t.Fatalf("message %q should mention %q", e.Message, want)
	}
}

func TestDragAndTypeValidation(t *testing.T) {
	c := newCtl(newFake(1280, 720))
	_, _ = c.Do(context.Background(), Request{Action: "screenshot"})
	if _, err := c.Do(context.Background(), Request{Action: "drag", Path: []Point{{1, 1}}}); codeOf(t, err) != CodeInvalidArguments {
		t.Errorf("one-point drag: %v", err)
	}
	if _, err := c.Do(context.Background(), Request{Action: "type", Text: "a\x1bb"}); codeOf(t, err) != CodeInvalidArguments {
		t.Errorf("control char: %v", err)
	}
	if _, err := c.Do(context.Background(), Request{Action: "key", Keys: "ctrl"}); codeOf(t, err) != CodeInvalidArguments {
		t.Errorf("modifier-only combo: %v", err)
	}
	if _, err := c.Do(context.Background(), Request{Action: "nope"}); codeOf(t, err) != CodeInvalidArguments {
		t.Errorf("unknown action: %v", err)
	}
}

func TestSessionBusyWhenAnotherActionRuns(t *testing.T) {
	c := New(newFake(1280, 720), WithSettle(0))
	c.mu.Lock() // simulate an in-flight action
	defer c.mu.Unlock()
	_, err := c.Do(context.Background(), Request{Action: "screenshot"})
	if codeOf(t, err) != CodeSessionBusy {
		t.Fatalf("got %v", err)
	}
}

func TestParseComboAndDestructive(t *testing.T) {
	for combo, want := range map[string]bool{
		"delete": true, "ctrl+w": true, "alt+f4": true, "win+l": true, "cmd+l": true, "ctrl+x": true,
		"ctrl+c": false, "enter": false, "ctrl+shift+tab": false, "backspace": false,
	} {
		if got := isDestructiveCombo(combo); got != want {
			t.Errorf("isDestructiveCombo(%q) = %v, want %v", combo, got, want)
		}
	}
	for _, bad := range []string{"", "ctrl", "ctrl++a", "a+b", "ctrl+nonsense"} {
		if _, _, err := parseCombo(bad); err == nil {
			t.Errorf("parseCombo(%q) should fail", bad)
		}
	}
}
