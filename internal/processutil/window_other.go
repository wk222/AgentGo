//go:build !windows

package processutil

import "os/exec"

func HideWindow(cmd *exec.Cmd) {}
