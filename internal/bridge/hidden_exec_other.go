//go:build !windows

package bridge

import "os/exec"

func hideExecWindow(cmd *exec.Cmd) {}
