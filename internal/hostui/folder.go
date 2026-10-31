package hostui

import (
	"bytes"
	"context"
	"encoding/base64"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// PickFolder shows a native folder dialog and returns the chosen path, or "" if
// the user cancelled. On Windows it drives System.Windows.Forms through
// PowerShell (present since Windows 7), so no extra DLLs are needed.
func PickFolder(ctx context.Context, title, startDir string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", ErrFolderPickerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA",
		"-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePS(folderScript(title, startDir)))
	hideWindow(cmd)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", &pickError{msg: msg}
		}
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(out.String(), "\ufeff")), nil
}

// folderScript is the PowerShell that shows the dialog. A tiny topmost owner
// form keeps the dialog in front of the browser window.
func folderScript(title, startDir string) string {
	return `$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$owner.StartPosition = 'CenterScreen'
$owner.Size = New-Object System.Drawing.Size(1,1)
$owner.Show()
$owner.Activate()
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = ` + psQuote(title) + `
$d.ShowNewFolderButton = $true
` + startDirLine(startDir) + `
$r = $d.ShowDialog($owner)
$owner.Close()
if ($r -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
  [Console]::Out.Write($d.SelectedPath)
}`
}

type pickError struct{ msg string }

func (e *pickError) Error() string { return e.msg }

func startDirLine(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return "$d.SelectedPath = " + psQuote(dir)
}

// psQuote renders s as a PowerShell single-quoted string literal.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// encodePS encodes a script for powershell -EncodedCommand (UTF-16LE, base64).
func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
