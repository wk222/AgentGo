//go:build windows

package computeruse

import (
	"fmt"
	"image"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 substrate: GDI capture + SendInput, pure Go (no CGO). Adapted from
// octo-agent internal/computer/windows.go (MIT); the preflight checks for the
// secure desktop and for UIPI-blocked targets are new.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procSendInput                     = user32.NewProc("SendInput")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procVkKeyScanW                    = user32.NewProc("VkKeyScanW")
	procOpenInputDesktop              = user32.NewProc("OpenInputDesktop")
	procGetUserObjectInformationW     = user32.NewProc("GetUserObjectInformationW")
	procCloseDesktop                  = user32.NewProc("CloseDesktop")
	procWindowFromPoint               = user32.NewProc("WindowFromPoint")
	procGetAncestor                   = user32.NewProc("GetAncestor")

	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010
	mouseeventfWheel     = 0x0800
	mouseeventfHWheel    = 0x1000

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfUnicode     = 0x0004

	wheelDelta = 120

	smCxScreen = 0
	smCyScreen = 1

	srccopy      = 0x00CC0020
	captureblt   = 0x40000000
	biRGB        = 0
	dibRGBColors = 0

	desktopReadObjects = 0x0001
	uoiName            = 2
	gaRoot             = 2

	dragStepsPerSegment = 12
	dragStepDelay       = 8 * time.Millisecond
)

const (
	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkPrior   = 0x21
	vkNext    = 0x22
	vkEnd     = 0x23
	vkHome    = 0x24
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28
	vkDelete  = 0x2E
	vkLWin    = 0x5B
	vkF1      = 0x70
)

// dpiAwarenessContextPerMonitorAwareV2 is DPI_AWARENESS_CONTEXT(-4).
const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)

type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	dwFlags   uint32
	time      uint32
	extraInfo uintptr
}

type keybdInput struct {
	wVk, wScan uint16
	dwFlags    uint32
	time       uint32
	extraInfo  uintptr
}

// input mirrors INPUT: a DWORD type then a union whose largest member is
// MOUSEINPUT (pointer-aligned, hence the padding on 64-bit).
type input struct {
	typ uint32
	_   [unsafe.Sizeof(uintptr(0)) - 4]byte
	mi  mouseInput
}

func (in *input) keyboard() *keybdInput { return (*keybdInput)(unsafe.Pointer(&in.mi)) }

type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

var dpiOnce sync.Once

// ensureDPIAware opts the process into per-monitor-v2 awareness so
// GetSystemMetrics, SetCursorPos and the GDI capture all speak physical
// pixels. An unaware process on a 200% display reads half-size everything and
// every coordinate lands 2x off, silently.
func ensureDPIAware() {
	dpiOnce.Do(func() {
		if procSetProcessDpiAwarenessContext.Find() == nil {
			procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
		}
	})
}

// NewSubstrate returns the Win32 substrate.
func NewSubstrate() Substrate { ensureDPIAware(); return winSubstrate{} }

// Supported reports whether this build has a desktop substrate.
func Supported() bool { return true }

type winSubstrate struct{}

func (winSubstrate) ScreenSize() (int, int, error) {
	ensureDPIAware()
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("GetSystemMetrics reported a %dx%d primary display", w, h)
	}
	return int(int32(w)), int(int32(h)), nil
}

// Capture grabs the primary display through GDI (BitBlt into a compatible
// bitmap, GetDIBits as top-down 32-bit BGRA) and returns it as RGBA.
func (s winSubstrate) Capture() (*image.RGBA, error) {
	w, h, err := s.ScreenSize()
	if err != nil {
		return nil, err
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC(screen) failed")
	}
	defer procReleaseDC.Call(0, screen)

	mem, _, _ := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(mem)

	bmp, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap(%dx%d) failed", w, h)
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(mem, bmp)
	ok, _, e := procBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), screen, 0, 0, srccopy|captureblt)
	// GetDIBits needs the bitmap deselected from every DC.
	procSelectObject.Call(mem, old)
	if ok == 0 {
		return nil, fmt.Errorf("BitBlt failed: %v", e)
	}

	bi := bitmapInfo{header: bitmapInfoHeader{
		size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), width: int32(w), height: -int32(h),
		planes: 1, bitCount: 32, compression: biRGB,
	}}
	buf := make([]byte, w*h*4)
	lines, _, e := procGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
	if int32(lines) == 0 {
		return nil, fmt.Errorf("GetDIBits failed: %v", e)
	}
	for i := 0; i+3 < len(buf); i += 4 { // BGRA -> RGBA in place, opaque
		buf[i], buf[i+2] = buf[i+2], buf[i]
		buf[i+3] = 0xFF
	}
	return &image.RGBA{Pix: buf, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
}

// Preflight refuses what the OS would drop silently. SendInput reports success
// when UIPI discards input aimed at a higher-integrity window, and does nothing
// useful while the secure desktop (UAC prompt, lock screen) is showing.
func (winSubstrate) Preflight(hasPoint bool, x, y float64) *Error {
	if !inputDesktopIsDefault() {
		return NewError(CodeSecureDesktop,
			"the secure desktop is active (a UAC prompt or the lock screen); input cannot reach it. Ask the user to deal with it, then take a screenshot")
	}
	if hasPoint && targetElevated(x, y) {
		return NewError(CodeTargetElevated,
			"the window under (%.0f, %.0f) runs elevated and this process does not, so Windows drops the input. Ask the user to run agentgo as administrator or to use another window", x, y)
	}
	return nil
}

func inputDesktopIsDefault() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		return false // access denied: the input desktop belongs to SYSTEM (secure desktop)
	}
	defer procCloseDesktop.Call(h)
	name := make([]uint16, 64)
	var need uint32
	ok, _, _ := procGetUserObjectInformationW.Call(h, uoiName, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&need)))
	if ok == 0 {
		return true // cannot tell; do not block
	}
	return strings.EqualFold(windows.UTF16ToString(name), "Default")
}

func targetElevated(x, y float64) bool {
	px, py := int32(x), int32(y)
	var hwnd uintptr
	if unsafe.Sizeof(uintptr(0)) == 8 { // POINT is passed by value packed into one register
		hwnd, _, _ = procWindowFromPoint.Call(uintptr(uint32(px)) | uintptr(uint32(py))<<32)
	} else {
		hwnd, _, _ = procWindowFromPoint.Call(uintptr(px), uintptr(py))
	}
	if hwnd == 0 {
		return false
	}
	if root, _, _ := procGetAncestor.Call(hwnd, gaRoot); root != 0 {
		hwnd = root
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 || pid == windows.GetCurrentProcessId() {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return integrityRID(tok) > integrityRID(windows.GetCurrentProcessToken())
}

// integrityRID returns the token's mandatory integrity level RID (0x2000
// medium, 0x3000 high, ...), or 0 when unreadable.
func integrityRID(tok windows.Token) uint32 {
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, nil, 0, &n)
	if n == 0 {
		return 0
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, &buf[0], n, &n); err != nil {
		return 0
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	sid := label.Label.Sid
	if sid == nil {
		return 0
	}
	cnt := sid.SubAuthorityCount()
	if cnt == 0 {
		return 0
	}
	return sid.SubAuthority(uint32(cnt - 1))
}

func sendInput(ins []input) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, e := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(input{}))
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput delivered %d of %d events: %v", n, len(ins), e)
	}
	return nil
}

func setCursor(x, y float64) error {
	ensureDPIAware()
	if ok, _, e := procSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y))); ok == 0 {
		return fmt.Errorf("SetCursorPos(%.0f, %.0f) failed: %v", x, y, e)
	}
	return nil
}

func mouse(flags uint32, data int32) input {
	return input{typ: inputMouse, mi: mouseInput{dwFlags: flags, mouseData: uint32(data)}}
}

func (winSubstrate) MoveTo(x, y float64) error { return setCursor(x, y) }

func (winSubstrate) Click(button string, x, y float64, clicks int) error {
	if err := setCursor(x, y); err != nil {
		return err
	}
	down, up := uint32(mouseeventfLeftDown), uint32(mouseeventfLeftUp)
	if button == "right" {
		down, up = mouseeventfRightDown, mouseeventfRightUp
	}
	ins := make([]input, 0, clicks*2)
	for i := 0; i < clicks; i++ {
		ins = append(ins, mouse(down, 0), mouse(up, 0))
	}
	return sendInput(ins)
}

// Scroll posts wheel events at the cursor. A positive WHEEL_DELTA rotates the
// wheel away from the user, so dy is negated to keep "positive = down".
func (winSubstrate) Scroll(dx, dy float64) error {
	var ins []input
	if dy != 0 {
		ins = append(ins, mouse(mouseeventfWheel, int32(-dy*wheelDelta)))
	}
	if dx != 0 {
		ins = append(ins, mouse(mouseeventfHWheel, int32(dx*wheelDelta)))
	}
	return sendInput(ins)
}

// Drag holds the left button along path with interpolated moves, the shape
// sliders, crop boxes and brushes need (a press-release teleport is not a drag).
func (winSubstrate) Drag(path []Point) (err error) {
	if len(path) < 2 {
		return fmt.Errorf("drag needs at least two points")
	}
	if err = setCursor(path[0].X, path[0].Y); err != nil {
		return err
	}
	if err = sendInput([]input{mouse(mouseeventfLeftDown, 0)}); err != nil {
		return err
	}
	defer func() { // never leave the button stuck down
		if upErr := sendInput([]input{mouse(mouseeventfLeftUp, 0)}); err == nil {
			err = upErr
		}
	}()
	for i := 1; i < len(path); i++ {
		a, b := path[i-1], path[i]
		for s := 1; s <= dragStepsPerSegment; s++ {
			t := float64(s) / dragStepsPerSegment
			if err = setCursor(a.X+(b.X-a.X)*t, a.Y+(b.Y-a.Y)*t); err != nil {
				return err
			}
			time.Sleep(dragStepDelay)
		}
	}
	return nil
}

// TypeText injects each UTF-16 unit as KEYEVENTF_UNICODE, independent of the
// keyboard layout. Newlines and tabs become real Enter/Tab presses because
// edit controls act on VK_RETURN/VK_TAB, not on literal control characters.
func (s winSubstrate) TypeText(text string) error {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var run []rune
	flush := func() error {
		if len(run) == 0 {
			return nil
		}
		err := typeUnits(string(run))
		run = run[:0]
		return err
	}
	for _, r := range text {
		switch r {
		case '\n', '\r':
			if err := flush(); err != nil {
				return err
			}
			if err := s.Press("enter"); err != nil {
				return err
			}
		case '\t':
			if err := flush(); err != nil {
				return err
			}
			if err := s.Press("tab"); err != nil {
				return err
			}
		default:
			run = append(run, r)
		}
	}
	return flush()
}

func typeUnits(s string) error {
	units := utf16.Encode([]rune(s))
	const batch = 64 // small batches so one lost batch does not drop a paragraph
	for start := 0; start < len(units); start += batch {
		end := min(start+batch, len(units))
		ins := make([]input, 0, (end-start)*2)
		for _, u := range units[start:end] {
			var d, up input
			d.typ, up.typ = inputKeyboard, inputKeyboard
			kd, ku := d.keyboard(), up.keyboard()
			kd.wScan, kd.dwFlags = u, keyeventfUnicode
			ku.wScan, ku.dwFlags = u, keyeventfUnicode|keyeventfKeyUp
			ins = append(ins, d, up)
		}
		if err := sendInput(ins); err != nil {
			return err
		}
	}
	return nil
}

var winNamedKeys = map[string]struct {
	vk  uint16
	ext bool
}{
	"enter": {vkReturn, false}, "tab": {vkTab, false}, "space": {vkSpace, false},
	"backspace": {vkBack, false}, "delete": {vkDelete, true}, "escape": {vkEscape, false},
	"left": {vkLeft, true}, "right": {vkRight, true}, "down": {vkDown, true}, "up": {vkUp, true},
	"home": {vkHome, true}, "end": {vkEnd, true}, "pageup": {vkPrior, true}, "pagedown": {vkNext, true},
	"f1": {vkF1, false}, "f2": {vkF1 + 1, false}, "f3": {vkF1 + 2, false}, "f4": {vkF1 + 3, false},
	"f5": {vkF1 + 4, false}, "f6": {vkF1 + 5, false}, "f7": {vkF1 + 6, false}, "f8": {vkF1 + 7, false},
	"f9": {vkF1 + 8, false}, "f10": {vkF1 + 9, false}, "f11": {vkF1 + 10, false}, "f12": {vkF1 + 11, false},
}

// winKeyCode resolves a canonical key name; single characters go through
// VkKeyScanW so the current layout decides (its high byte bit 0 = needs Shift).
func winKeyCode(key string) (vk uint16, ext, needShift bool, err error) {
	if k, ok := winNamedKeys[key]; ok {
		return k.vk, k.ext, false, nil
	}
	if len(key) != 1 {
		return 0, false, false, fmt.Errorf("key %q has no Windows virtual-key code", key)
	}
	r, _, _ := procVkKeyScanW.Call(uintptr(key[0]))
	scan := int16(r)
	if scan == -1 {
		return 0, false, false, fmt.Errorf("character %q is not on the current keyboard layout", key)
	}
	return uint16(scan & 0xFF), false, scan>>8&1 != 0, nil
}

func keyEvent(vk uint16, flags uint32) input {
	var in input
	in.typ = inputKeyboard
	k := in.keyboard()
	k.wVk, k.dwFlags = vk, flags
	return in
}

// Press chords the modifiers around one key: modifiers down, key down/up,
// modifiers up in reverse.
func (winSubstrate) Press(combo string) error {
	key, flags, err := parseCombo(combo)
	if err != nil {
		return err
	}
	vk, ext, needShift, err := winKeyCode(key)
	if err != nil {
		return err
	}
	var mods []uint16
	if flags&flagShift != 0 || needShift {
		mods = append(mods, vkShift)
	}
	if flags&flagControl != 0 {
		mods = append(mods, vkControl)
	}
	if flags&flagAlt != 0 {
		mods = append(mods, vkMenu)
	}
	if flags&flagWin != 0 {
		mods = append(mods, vkLWin)
	}
	var keyFlags uint32
	if ext {
		keyFlags = keyeventfExtendedKey
	}
	ins := make([]input, 0, len(mods)*2+2)
	for _, m := range mods {
		ins = append(ins, keyEvent(m, 0))
	}
	ins = append(ins, keyEvent(vk, keyFlags), keyEvent(vk, keyFlags|keyeventfKeyUp))
	for i := len(mods) - 1; i >= 0; i-- {
		ins = append(ins, keyEvent(mods[i], keyeventfKeyUp))
	}
	return sendInput(ins)
}
