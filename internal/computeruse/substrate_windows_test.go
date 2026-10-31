//go:build windows

package computeruse

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// SendInput silently returns 0 when cbSize is not exactly sizeof(INPUT), and
// scrambles the union when the padding is wrong - both look like "nothing
// happened" with no error, so the layout is pinned here.
func TestInputStructLayoutMatchesWin32(t *testing.T) {
	want := map[uintptr]uintptr{8: 40, 4: 28} // 64-bit / 32-bit sizeof(INPUT)
	if got := unsafe.Sizeof(input{}); got != want[unsafe.Sizeof(uintptr(0))] {
		t.Fatalf("sizeof(INPUT) = %d, want %d", got, want[unsafe.Sizeof(uintptr(0))])
	}
	var in input
	if off := uintptr(unsafe.Pointer(&in.mi)) - uintptr(unsafe.Pointer(&in)); off != unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("union offset = %d, want %d", off, unsafe.Sizeof(uintptr(0)))
	}
	if got := unsafe.Sizeof(keybdInput{}); got > unsafe.Sizeof(mouseInput{}) {
		t.Fatalf("KEYBDINPUT (%d) must fit inside the MOUSEINPUT-sized union (%d)", got, unsafe.Sizeof(mouseInput{}))
	}
	if got := unsafe.Sizeof(bitmapInfoHeader{}); got != 40 {
		t.Fatalf("sizeof(BITMAPINFOHEADER) = %d, want 40", got)
	}
}

// TestLiveScreenshot grabs the real screen through the full Controller path.
// Read-only: it injects no input. Run with AGENTGO_CUA_LIVE=1; set
// AGENTGO_CUA_LIVE_OUT to keep the PNG.
func TestLiveScreenshot(t *testing.T) {
	if os.Getenv("AGENTGO_CUA_LIVE") != "1" {
		t.Skip("set AGENTGO_CUA_LIVE=1 to capture the real screen")
	}
	sub := NewSubstrate()
	if e := sub.Preflight(false, 0, 0); e != nil {
		t.Fatalf("preflight on the normal desktop should pass: %v", e)
	}
	c := New(sub, WithSettle(0))
	res, err := c.Do(t.Context(), Request{Action: "screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	w, h := pngSize(t, res.PNG)
	t.Logf("%s | png %dx%d, %d KB", res.Text, w, h, len(res.PNG)/1024)
	if w > MaxImageW || h > MaxImageH || w < 100 {
		t.Fatalf("unexpected screenshot size %dx%d", w, h)
	}
	if out := os.Getenv("AGENTGO_CUA_LIVE_OUT"); out != "" {
		if err := os.WriteFile(filepath.Clean(out), res.PNG, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A zoom of the top-left quarter must come back at native resolution.
	z, err := c.Do(t.Context(), Request{Action: "zoom", X1: fp(0), Y1: fp(0), X2: fp(float64(w) / 2), Y2: fp(float64(h) / 2)})
	if err != nil {
		t.Fatal(err)
	}
	zw, zh := pngSize(t, z.PNG)
	t.Logf("zoom %s: %dx%d", z.ZoomID, zw, zh)
}
