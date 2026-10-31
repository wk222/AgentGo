//go:build windows

package computeruse

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StopHotkeyLabel is the chord that stops computer use. It is a real keypress
// the OS delivers to this process, so nothing the model injects can press it
// on the user's behalf and nothing the model sees tells it the chord.
const StopHotkeyLabel = "Ctrl+Alt+Esc"

const (
	modAlt       = 0x0001
	modControl   = 0x0002
	modNoRepeat  = 0x4000
	wmHotkey     = 0x0312
	wmQuit       = 0x0012
	hotkeyID     = 0x4147 // arbitrary, per-thread
	pmNoRemove   = 0x0000
	vkEscapeCode = 0x1B
)

var (
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

// StartStopHotkey registers the global stop chord on a dedicated OS thread (a
// hotkey is delivered to the thread's message queue, so that thread must stay
// pinned and pump messages). The returned func unregisters it.
func StartStopHotkey(c *Controller) (func(), error) {
	type started struct {
		tid uint32
		err error
	}
	ready := make(chan started, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var m winMsg
		// Touch the queue so PostThreadMessage to this thread can succeed.
		procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmNoRemove)
		ok, _, e := procRegisterHotKey.Call(0, hotkeyID, modControl|modAlt|modNoRepeat, vkEscapeCode)
		if ok == 0 {
			ready <- started{err: fmt.Errorf("%s is held by another app: %v", StopHotkeyLabel, e)}
			return
		}
		defer procUnregisterHotKey.Call(0, hotkeyID)
		ready <- started{tid: windows.GetCurrentThreadId()}
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT or error
				return
			}
			if m.message == wmHotkey && m.wParam == hotkeyID {
				c.Stop()
			}
		}
	}()
	st := <-ready
	if st.err != nil {
		return func() {}, st.err
	}
	return func() {
		procPostThreadMessageW.Call(uintptr(st.tid), wmQuit, 0, 0)
		<-done
	}, nil
}
