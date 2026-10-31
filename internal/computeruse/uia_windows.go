//go:build windows

package computeruse

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows UI Automation over raw COM vtables: no cgo and no COM helper
// dependency (the vtable layout approach is adapted from octo-agent's
// internal/computer, MIT). Behavioural choices that come from wechat-ui-go's
// uia package: the tree is walked live (no CacheRequest, which returns empty
// properties for Qt/Chromium), every element is released as soon as it has
// been read, and an element that went stale mid-walk is skipped rather than
// failing the whole call.
//
// Slot numbers are positions in the vtables of uiautomationclient.h, with
// IUnknown occupying 0..2.

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysAllocString   = oleaut32.NewProc("SysAllocString")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procSysStringLen     = oleaut32.NewProc("SysStringLen")
	procSystemParamInfo  = user32.NewProc("SystemParametersInfoW")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetClassNameW    = user32.NewProc("GetClassNameW")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic("computeruse: bad GUID literal " + s)
	}
	return g
}

var (
	clsidCUIAutomation = mustGUID("{ff48dba4-60ef-4201-aa87-54103eef594e}")
	iidIUIAutomation   = mustGUID("{30cbe57d-d9d0-452a-ab13-7ac5ac4825ee}")

	iidInvokePattern        = mustGUID("{fb377fbe-8ea6-46d5-9c73-6499642d3059}")
	iidValuePattern         = mustGUID("{a94cd8b1-0844-4cd6-9d2d-640537ab39e9}")
	iidTogglePattern        = mustGUID("{94cf8058-9b8d-4ab9-8bfd-4cd0a33c8c70}")
	iidSelectionItemPattern = mustGUID("{a8efa66a-0fda-421a-9194-38021f2797b1}")
	iidExpandCollapse       = mustGUID("{619be086-1f4e-4ee4-bafa-210128738730}")
	iidLegacyPattern        = mustGUID("{828055ad-355b-4435-86d5-3b51c14a9b1b}")
)

const (
	patInvoke        = 10000
	patValue         = 10002
	patExpandCollaps = 10005
	patSelectionItem = 10010
	patToggle        = 10015
	patLegacy        = 10018

	firstControlTypeID = 50000

	hrFalse         = 0x00000001
	hrChangedMode   = 0x80010106
	hrAccessDenied  = 0x80070005
	spiGetScreenRdr = 0x0046
	spiSetScreenRdr = 0x0047
	spifSendChange  = 0x0002

	slotQI      = 0
	slotRelease = 2

	slotAutoElementFromHandle = 6
	slotAutoControlWalker     = 14

	slotElSetFocus         = 3
	slotElGetPattern       = 16
	slotElControlType      = 21
	slotElName             = 23
	slotElIsEnabled        = 28
	slotElAutomationID     = 29
	slotElClassName        = 30
	slotElHelpText         = 31
	slotElIsOffscreen      = 38
	slotElBoundingRect     = 43
	slotWalkFirstChild     = 4
	slotWalkNextSibling    = 6
	slotInvoke             = 3
	slotValueSet           = 3
	slotValueGet           = 4
	slotToggle             = 3
	slotSelect             = 3
	slotExpand             = 3
	slotLegacyDefaultAct   = 4
	slotLegacySetValue     = 5
	activationRetryDelay   = 600 * time.Millisecond
	activationMinNodeCount = 3
)

var controlTypeNames = [...]string{
	"Button", "Calendar", "CheckBox", "ComboBox", "Edit", "Hyperlink", "Image",
	"ListItem", "List", "Menu", "MenuBar", "MenuItem", "ProgressBar", "RadioButton",
	"ScrollBar", "Slider", "Spinner", "StatusBar", "Tab", "TabItem", "Text", "ToolBar",
	"ToolTip", "Tree", "TreeItem", "Custom", "Group", "Thumb", "DataGrid", "DataItem",
	"Document", "SplitButton", "Window", "Pane", "Header", "HeaderItem", "Table",
	"TitleBar", "Separator", "SemanticZoom", "AppBar",
}

func controlTypeName(id int32) string {
	if i := int(id) - firstControlTypeID; i >= 0 && i < len(controlTypeNames) {
		return controlTypeNames[i]
	}
	return fmt.Sprintf("ControlType%d", id)
}

// comObj is a raw COM interface pointer.
type comObj uintptr

// call invokes vtable slot n. The dereferences go through the address of a Go
// variable so no uintptr is converted to unsafe.Pointer (go vet unsafeptr).
func (o comObj) call(slot int, args ...uintptr) uintptr {
	words := *(**[1]uintptr)(unsafe.Pointer(&o))
	vtbl := *(**[128]uintptr)(unsafe.Pointer(&words[0]))
	r, _, _ := syscall.SyscallN(vtbl[slot], append([]uintptr{uintptr(o)}, args...)...)
	return r
}

func (o comObj) release() {
	if o != 0 {
		o.call(slotRelease)
	}
}

func (o comObj) queryInterface(iid *windows.GUID) comObj {
	var out uintptr
	if failed(o.call(slotQI, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return comObj(out)
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func hresultErr(what string, hr uintptr) error {
	if !failed(hr) {
		return fmt.Errorf("%s returned nothing", what)
	}
	if uint32(hr) == hrAccessDenied {
		return NewError(CodeTargetElevated,
			"%s was denied: the target window runs at a higher privilege level than agentgo. Relaunch agentgo (or the agentgo-cua sidecar) as administrator, or ask the user to do this step", what)
	}
	return fmt.Errorf("%s failed (HRESULT 0x%08x)", what, uint32(hr))
}

func bstrToString(b uintptr) string {
	if b == 0 {
		return ""
	}
	r, _, _ := procSysStringLen.Call(b)
	n := uint32(r)
	if n == 0 {
		return ""
	}
	p := *(**uint16)(unsafe.Pointer(&b))
	return windows.UTF16ToString(unsafe.Slice(p, n))
}

func freeBSTR(b uintptr) {
	if b != 0 {
		procSysFreeString.Call(b)
	}
}

// withCOM runs fn on a locked OS thread with COM initialised and a
// CUIAutomation instance. S_FALSE (already initialised) is balanced by
// CoUninitialize; RPC_E_CHANGED_MODE means a different apartment is already
// set up, which UIA tolerates - use it and leave its lifetime alone.
func withCOM(fn func(auto comObj) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit := true
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		switch {
		case errors.Is(err, syscall.Errno(hrFalse)):
		case errors.Is(err, syscall.Errno(hrChangedMode)):
			uninit = false
		default:
			return fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	if uninit {
		defer windows.CoUninitialize()
	}
	var raw uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&raw)))
	if failed(hr) || raw == 0 {
		return hresultErr("CoCreateInstance(CUIAutomation)", hr)
	}
	auto := comObj(raw)
	defer auto.release()
	return fn(auto)
}

func elString(el comObj, slot int) string {
	var b uintptr
	if failed(el.call(slot, uintptr(unsafe.Pointer(&b)))) {
		return ""
	}
	defer freeBSTR(b)
	return bstrToString(b)
}

func elBool(el comObj, slot int) bool {
	var v int32
	if failed(el.call(slot, uintptr(unsafe.Pointer(&v)))) {
		return false
	}
	return v != 0
}

func elPattern(el comObj, id uintptr, iid *windows.GUID) comObj {
	var unk uintptr
	if failed(el.call(slotElGetPattern, id, uintptr(unsafe.Pointer(&unk)))) || unk == 0 {
		return 0
	}
	u := comObj(unk)
	defer u.release()
	return u.queryInterface(iid)
}

func readNode(el comObj, id, depth int) UINode {
	n := UINode{
		ID:           id,
		Depth:        depth,
		Name:         elString(el, slotElName),
		AutomationID: elString(el, slotElAutomationID),
		Class:        elString(el, slotElClassName),
		Help:         elString(el, slotElHelpText),
		Enabled:      elBool(el, slotElIsEnabled),
	}
	var ct int32
	if !failed(el.call(slotElControlType, uintptr(unsafe.Pointer(&ct)))) {
		n.Role = controlTypeName(ct)
	}
	var rc windows.Rect
	if !failed(el.call(slotElBoundingRect, uintptr(unsafe.Pointer(&rc)))) {
		n.Rect = UIRect{int(rc.Left), int(rc.Top), int(rc.Right - rc.Left), int(rc.Bottom - rc.Top)}
	}
	return n
}

// ---- window resolution ----------------------------------------------------

var (
	enumOnce sync.Once
	enumCB   uintptr
	enumMu   sync.Mutex
	enumWant string
	enumHits []enumHit
)

type enumHit struct {
	hwnd  uintptr
	title string
}

func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// resolveWindow returns the top-level window whose title contains want
// (case-insensitive; an exact title wins), or the foreground window.
func resolveWindow(want string) (uintptr, string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		h := uintptr(windows.GetForegroundWindow())
		if h == 0 {
			return 0, "", NewError(CodeInvalidArguments, "there is no foreground window; pass window with part of a window title")
		}
		return h, windowTitle(h), nil
	}
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOnce.Do(func() {
		enumCB = syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
			if !windows.IsWindowVisible(windows.HWND(h)) {
				return 1
			}
			t := windowTitle(h)
			if t != "" && strings.Contains(strings.ToLower(t), enumWant) {
				enumHits = append(enumHits, enumHit{h, t})
			}
			return 1
		})
	})
	enumWant, enumHits = strings.ToLower(want), nil
	if err := windows.EnumWindows(enumCB, nil); err != nil {
		return 0, "", fmt.Errorf("EnumWindows: %w", err)
	}
	if len(enumHits) == 0 {
		return 0, "", NewError(CodeInvalidArguments, "no visible window has a title containing %q; take a screenshot to see what is open", want)
	}
	for _, h := range enumHits { // EnumWindows is z-order: first exact match wins
		if strings.EqualFold(h.title, want) {
			return h.hwnd, h.title, nil
		}
	}
	return enumHits[0].hwnd, enumHits[0].title, nil
}

// ---- tree walk ------------------------------------------------------------

// walkTree visits the live tree depth-first. visit returning false stops the
// whole walk. Offscreen subtrees are skipped (hidden tabs, collapsed panes),
// which keeps both the digest and element ids focused on what is on screen.
// It returns the number of elements visited.
func walkTree(auto, root comObj, maxDepth int, visit func(n UINode, el comObj) bool) (int, error) {
	var walker uintptr
	if hr := auto.call(slotAutoControlWalker, uintptr(unsafe.Pointer(&walker))); failed(hr) || walker == 0 {
		return 0, hresultErr("IUIAutomation::ControlViewWalker", hr)
	}
	w := comObj(walker)
	defer w.release()

	index, stop := 0, false
	var rec func(el comObj, depth int)
	rec = func(el comObj, depth int) {
		if stop || index >= maxUINodes {
			return
		}
		if depth > 0 && elBool(el, slotElIsOffscreen) {
			return
		}
		node := readNode(el, index, depth)
		index++
		if !visit(node, el) {
			stop = true
			return
		}
		if depth >= maxDepth {
			return
		}
		var child uintptr
		if failed(w.call(slotWalkFirstChild, uintptr(el), uintptr(unsafe.Pointer(&child)))) {
			return
		}
		for i := 0; child != 0 && !stop && i < maxUIChildren; i++ {
			c := comObj(child)
			rec(c, depth+1)
			var next uintptr
			if failed(w.call(slotWalkNextSibling, child, uintptr(unsafe.Pointer(&next)))) {
				next = 0
			}
			c.release()
			child = next
		}
		if child != 0 { // hit the fan-out cap: release the unvisited remainder
			comObj(child).release()
		}
	}
	rec(root, 0)
	return index, nil
}

// looksUnbuilt reports whether a walk returned suspiciously little: nearly
// nothing for any window, or a modest count for a Chromium/Electron/Qt shell
// whose web or custom-drawn content is exposed only after a client announces
// itself as a screen reader.
func looksUnbuilt(hwnd uintptr, count int) bool {
	if count < activationMinNodeCount {
		return true
	}
	if count >= 60 {
		return false
	}
	buf := make([]uint16, 128)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	cls := windows.UTF16ToString(buf[:n])
	return strings.HasPrefix(cls, "Chrome_WidgetWin") || strings.HasPrefix(cls, "Qt") || strings.HasPrefix(cls, "Intermediate D3D")
}

// enableScreenReaderFlag turns on the system "screen reader is running" flag
// for the duration of an operation. Chromium/Electron and some Qt apps only
// build their accessibility tree when they see it; it is the same signal a
// screen reader gives. The previous value is restored.
func enableScreenReaderFlag() (restore func()) {
	var was int32
	procSystemParamInfo.Call(spiGetScreenRdr, 0, uintptr(unsafe.Pointer(&was)), 0)
	if was != 0 {
		return func() {}
	}
	procSystemParamInfo.Call(spiSetScreenRdr, 1, 0, spifSendChange)
	return func() { procSystemParamInfo.Call(spiSetScreenRdr, 0, 0, spifSendChange) }
}

// ---- Accessibility implementation ------------------------------------------

type winAccessibility struct{}

// NewAccessibility returns the Windows UI Automation channel.
func NewAccessibility() Accessibility { return winAccessibility{} }

// session resolves the window and runs fn with a root element. When the tree
// comes back nearly empty (the app has not built it yet) it switches the
// screen-reader flag on, waits, and lets fn run again.
func session(t UITarget, fn func(auto, root comObj, title string) (count int, err error)) error {
	hwnd, title, err := resolveWindow(t.Window)
	if err != nil {
		return err
	}
	return withCOM(func(auto comObj) error {
		var rootPtr uintptr
		if hr := auto.call(slotAutoElementFromHandle, hwnd, uintptr(unsafe.Pointer(&rootPtr))); failed(hr) || rootPtr == 0 {
			return hresultErr("IUIAutomation::ElementFromHandle", hr)
		}
		root := comObj(rootPtr)
		defer root.release()

		count, err := fn(auto, root, title)
		if err != nil || !looksUnbuilt(hwnd, count) {
			return err
		}
		restore := enableScreenReaderFlag()
		defer restore()
		time.Sleep(activationRetryDelay)
		_, err = fn(auto, root, title)
		return err
	})
}

func clampDepth(d int) int {
	switch {
	case d <= 0:
		return defaultUIDepth
	case d > maxUIDepth:
		return maxUIDepth
	}
	return d
}

func (winAccessibility) Tree(t UITarget, maxDepth int) (string, []UINode, error) {
	maxDepth = clampDepth(maxDepth)
	var nodes []UINode
	var title string
	err := session(t, func(auto, root comObj, ttl string) (int, error) {
		title, nodes = ttl, nodes[:0]
		return walkTree(auto, root, maxDepth, func(n UINode, _ comObj) bool {
			nodes = append(nodes, n)
			return true
		})
	})
	return title, nodes, err
}

func (winAccessibility) Act(t UITarget, maxDepth int, sel UISelector, act UIAction, value string) (UINode, string, error) {
	maxDepth = clampDepth(maxDepth)
	var (
		found  UINode
		note   string
		actErr error
		hit    bool
	)
	err := session(t, func(auto, root comObj, _ string) (int, error) {
		hit = false
		pass := func(exact bool) (int, error) {
			return walkTree(auto, root, maxDepth, func(n UINode, el comObj) bool {
				if sel.ID != nil {
					if n.ID != *sel.ID {
						return true
					}
				} else if !matchNode(sel, n, exact) {
					return true
				}
				hit, found = true, n
				note, actErr = doAction(el, n, act, value)
				return false
			})
		}
		count, err := pass(sel.needsTwoPasses())
		if err == nil && !hit && sel.needsTwoPasses() {
			count, err = pass(false)
		}
		return count, err
	})
	if err != nil {
		return UINode{}, "", err
	}
	if !hit {
		if sel.ID != nil {
			return UINode{}, "", NewError(CodeInvalidArguments,
				"no element with id %d in this window at max_depth %d: the tree changed or max_depth differs from the ui_tree that produced the id. Run ui_tree again", *sel.ID, maxDepth)
		}
		return UINode{}, "", NewError(CodeInvalidArguments,
			"no element matches role=%q name=%q name_exact=%q automation_id=%q. Run ui_tree to see what the window exposes (apps that expose no tree need pixel clicks)",
			sel.Role, sel.Name, sel.NameExact, sel.AutomationID)
	}
	return found, note, actErr
}

func doAction(el comObj, n UINode, act UIAction, value string) (string, error) {
	switch act {
	case UIFocus:
		if hr := el.call(slotElSetFocus); failed(hr) {
			return "", hresultErr("SetFocus", hr)
		}
		return "focused", nil
	case UIRead:
		if p := elPattern(el, patValue, &iidValuePattern); p != 0 {
			defer p.release()
			var b uintptr
			if !failed(p.call(slotValueGet, uintptr(unsafe.Pointer(&b)))) {
				defer freeBSTR(b)
				return bstrToString(b), nil
			}
		}
		return n.Name, nil // no value pattern: the label is the best reading
	case UISetValue:
		return setValue(el, value)
	case UIPress:
		if !n.Enabled {
			return "", NewError(CodeInvalidArguments, "the element is disabled, so pressing it would do nothing")
		}
		return press(el)
	}
	return "", fmt.Errorf("unknown ui action %q", act)
}

func press(el comObj) (string, error) {
	if p := elPattern(el, patInvoke, &iidInvokePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotInvoke); failed(hr) {
			return "", hresultErr("Invoke", hr)
		}
		return "invoked", nil
	}
	if p := elPattern(el, patToggle, &iidTogglePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotToggle); failed(hr) {
			return "", hresultErr("Toggle", hr)
		}
		return "toggled", nil
	}
	if p := elPattern(el, patSelectionItem, &iidSelectionItemPattern); p != 0 {
		defer p.release()
		if hr := p.call(slotSelect); failed(hr) {
			return "", hresultErr("Select", hr)
		}
		return "selected", nil
	}
	if p := elPattern(el, patExpandCollaps, &iidExpandCollapse); p != 0 {
		defer p.release()
		if hr := p.call(slotExpand); failed(hr) {
			return "", hresultErr("Expand", hr)
		}
		return "expanded", nil
	}
	if p := elPattern(el, patLegacy, &iidLegacyPattern); p != 0 {
		defer p.release()
		if hr := p.call(slotLegacyDefaultAct); failed(hr) {
			return "", hresultErr("DoDefaultAction", hr)
		}
		return "default action", nil
	}
	return "", ErrNoPattern
}

func setValue(el comObj, value string) (string, error) {
	wide, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return "", NewError(CodeInvalidArguments, "value: %v", err)
	}
	if p := elPattern(el, patValue, &iidValuePattern); p != 0 {
		defer p.release()
		b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(wide)))
		defer freeBSTR(b)
		if hr := p.call(slotValueSet, b); failed(hr) {
			return "", hresultErr("ValuePattern.SetValue", hr)
		}
		return "value set", nil
	}
	if p := elPattern(el, patLegacy, &iidLegacyPattern); p != 0 {
		defer p.release()
		if hr := p.call(slotLegacySetValue, uintptr(unsafe.Pointer(wide))); failed(hr) {
			return "", hresultErr("LegacyIAccessible.SetValue", hr)
		}
		return "value set", nil
	}
	return "", ErrNoPattern
}
