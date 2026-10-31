//go:build windows

package processutil

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideWindowPreservesOtherProcessAttributes(t *testing.T) {
	cmd := exec.Command("unused")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	HideWindow(cmd)
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != 0x08000200 {
		t.Fatalf("flags lost: %+v", cmd.SysProcAttr)
	}
	HideWindow(nil)
}
