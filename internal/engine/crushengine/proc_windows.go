//go:build windows

package crushengine

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// killTree also kills MCP/LSP grandchildren that Crush spawned.
func killTree(cmd *exec.Cmd) {
	k := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	hideWindow(k)
	if err := k.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
