//go:build windows

package linktest

import (
	"os/exec"
	"syscall"
)

func junction(link, target string) ([]byte, error) {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.CombinedOutput()
}

func shortName(path string) (string, error) {
	in, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
		if err != nil {
			return "", err
		}
		if int(n) <= len(buf) {
			return syscall.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}
