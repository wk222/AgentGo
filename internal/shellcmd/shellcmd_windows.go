//go:build windows

package shellcmd

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
)

var (
	utf8ConsoleOnce     sync.Once
	kernel32DLL         = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCP    = kernel32DLL.NewProc("SetConsoleCP")
	procSetConsoleOutCP = kernel32DLL.NewProc("SetConsoleOutputCP")
)

// ensureConsoleUTF8 switches the current Windows console codepage to 65001 (UTF-8)
// once per process (absorbed from a reference implementation)
// so child console processes inherit UTF-8 I/O without needing a wrapper binary.
func ensureConsoleUTF8() {
	utf8ConsoleOnce.Do(func() {
		_, _, _ = procSetConsoleCP.Call(65001)
		_, _, _ = procSetConsoleOutCP.Call(65001)
	})
}

// The line is passed raw. /S makes cmd strip exactly the outer quotes, so the
// line may itself start with a quote or contain any number of them.
func command(ctx context.Context, line string) *exec.Cmd {
	ensureConsoleUTF8()
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`, HideWindow: true}
	return cmd
}
