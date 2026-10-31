//go:build windows

package userhooks

import (
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")

const cpACP = 0 // CP_ACP: the system's ANSI code page (936, GBK, on a Chinese Windows)

// ansiToUTF8 reads s as text in the ANSI code page. Bytes the code page has no
// character for come out as its replacement character rather than failing.
func ansiToUTF8(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	src := unsafe.StringData(s)
	n, _, _ := procMultiByteToWideChar.Call(cpACP, 0, uintptr(unsafe.Pointer(src)), uintptr(len(s)), 0, 0)
	if n == 0 {
		return "", false
	}
	wide := make([]uint16, n)
	got, _, _ := procMultiByteToWideChar.Call(cpACP, 0, uintptr(unsafe.Pointer(src)), uintptr(len(s)), uintptr(unsafe.Pointer(&wide[0])), n)
	if got == 0 {
		return "", false
	}
	return string(utf16.Decode(wide[:got])), true
}
