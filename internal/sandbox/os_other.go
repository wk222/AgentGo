//go:build !darwin && !linux && !windows

package sandbox

import (
	"context"
	"os/exec"
)

// Available：本平台（Linux/macOS/Windows 之外，如 FreeBSD）没有可用的 OS 沙箱机制。
func Available() bool { return false }

// Command 在这里永远 fail-closed——要求沙箱的调用方不能悄悄无约束地运行。
func Command(_ context.Context, _ string, _ Policy) (*exec.Cmd, error) {
	return nil, ErrUnsupported
}
