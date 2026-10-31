//go:build !windows

package hostui

import "os/exec"

func hideWindow(*exec.Cmd) {}
