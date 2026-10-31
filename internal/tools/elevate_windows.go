//go:build windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func elevationSupported() bool { return true }

func isElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func powershellExe() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// runPowerShell runs script in a hidden PowerShell and waits for it. viaUAC
// starts it with the "runas" verb, which is what makes Windows apply its
// elevation policy (a prompt, a password, or nothing, as the system is set up).
// declined is true when the user answered the UAC prompt with "No".
func runPowerShell(ctx context.Context, script string, wait time.Duration, viaUAC bool) (code int, declined bool, err error) {
	params := "-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + encodePowerShell(script)
	if !viaUAC {
		return runPowerShellDirect(ctx, params, wait)
	}
	return runPowerShellRunAs(ctx, params, wait)
}

func runPowerShellDirect(ctx context.Context, params string, wait time.Duration) (int, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellExe())
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + powershellExe() + `" ` + params, HideWindow: true}
	err := cmd.Run()
	if err == nil {
		return 0, false, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), false, nil
	}
	if ctx.Err() != nil {
		return 1, false, fmt.Errorf("the command did not finish within %s", wait)
	}
	return 1, false, err
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	errorCancelled        = syscall.Errno(1223)
)

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func runPowerShellRunAs(ctx context.Context, params string, wait time.Duration) (int, bool, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(powershellExe())
	args, _ := windows.UTF16PtrFromString(params)
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: args,
		nShow:        windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(callErr, errorCancelled) {
			return 1, true, nil
		}
		return 1, false, fmt.Errorf("could not start the elevated process: %w", callErr)
	}
	defer windows.CloseHandle(info.hProcess)

	deadline := time.Now().Add(wait)
	for {
		ev, werr := windows.WaitForSingleObject(info.hProcess, 300)
		if werr != nil {
			return 1, false, werr
		}
		if ev == windows.WAIT_OBJECT_0 {
			break
		}
		if ctx.Err() != nil {
			return 1, false, errors.New("cancelled while the elevated command was running; it may still be running")
		}
		if time.Now().After(deadline) {
			// An elevated process cannot be ended from here: say so rather than pretend.
			return 1, false, fmt.Errorf("the command did not finish within %s; it may still be running with administrator rights", wait)
		}
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &exit); err != nil {
		return 1, false, err
	}
	return int(exit), false, nil
}
