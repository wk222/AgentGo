//go:build windows

package shellcmd

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// The line is passed raw. /S makes cmd strip exactly the outer quotes, so the
// line may itself start with a quote or contain any number of them.
func command(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`, HideWindow: true}
	return cmd
}

// Nothing to arrange: taskkill /T walks the tree by parent pid, which only works
// while the root is still alive, so KillTree must run before the root is killed.
func prepare(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	k := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = k.Run()
	_ = cmd.Process.Kill()
}
