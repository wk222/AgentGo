//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showFatalDialog shows a modal error box (MessageBoxW exists on Windows 7 and later).
func showFatalDialog(title, text string) {
	t, errTitle := syscall.UTF16PtrFromString(title)
	m, errText := syscall.UTF16PtrFromString(text)
	if errTitle != nil || errText != nil {
		return
	}
	const iconError, topmost = 0x10, 0x40000
	_, _, _ = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), iconError|topmost)
}
