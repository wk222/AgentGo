//go:build windows

package bridge

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hideExecWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
