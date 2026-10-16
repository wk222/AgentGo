//go:build !windows

package workspace

import "os/exec"

func HideExecWindow(cmd *exec.Cmd) {}
