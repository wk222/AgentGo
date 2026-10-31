package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool/utils"
)

// run_elevated: one command with Windows administrator rights.
//
// The user is in the loop twice: governance asks for approval on every call
// (never remembered, never auto-approved), and Windows shows its own UAC prompt
// when the system is set to ask. The tool never tries to switch UAC off, never
// keeps an elevated process around, and elevates exactly the command it was
// given: the command travels inside the launch parameters, not in a file another
// program could change while the prompt is on screen.

const (
	elevatedMaxCommand    = 4000
	elevatedDefaultWait   = 120 * time.Second
	elevatedMaxWait       = 10 * time.Minute
	elevatedMaxOutputRune = 30000
)

type elevatedInput struct {
	Command   string `json:"command" jsonschema:"required,description=Command line for cmd.exe (same syntax as execute_bash) to run with administrator rights. One command or a short chain with &"`
	Reason    string `json:"reason" jsonschema:"required,description=One sentence for the user: why administrator rights are needed. Shown on the approval card"`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=How long to wait in total, including the time the user needs for the UAC prompt (default 120000, max 600000)"`
}

type elevatedOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

const runElevatedDescription = "Run ONE command with Windows administrator rights (UAC). Windows only. " +
	"Use it only after the same command failed for lack of rights (\"Access is denied\", \"拒绝访问\", \"requires elevation\"); " +
	"never as a first attempt and never to get around a refusal. Every call needs the user's approval, and Windows may " +
	"ask the user to confirm a UAC prompt on this PC. The command runs in the workspace directory, with execute_bash " +
	"syntax (cmd.exe). Give a clear reason. Returns stdout, stderr and exit_code."

func registerRunElevated(r *Registry, workspaceRoot string) error {
	if !elevationSupported() {
		return nil
	}
	root := workspaceRoot
	t, err := utils.InferTool("run_elevated", runElevatedDescription,
		func(ctx context.Context, in elevatedInput) (elevatedOutput, error) {
			return runElevatedCommand(ctx, root, in), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func runElevatedCommand(ctx context.Context, root string, in elevatedInput) elevatedOutput {
	line := strings.TrimSpace(in.Command)
	switch {
	case line == "":
		return elevatedOutput{ExitCode: 1, Error: "empty command"}
	case utf8.RuneCountInString(line) > elevatedMaxCommand:
		return elevatedOutput{ExitCode: 1, Error: fmt.Sprintf("command is longer than %d characters; split it or run a script that already exists", elevatedMaxCommand)}
	}
	wait := elevatedDefaultWait
	if in.TimeoutMS > 0 {
		wait = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	if wait > elevatedMaxWait {
		wait = elevatedMaxWait
	}
	return runElevatedLine(ctx, root, line, wait, !isElevated())
}

// runElevatedLine runs line through the elevation script. viaUAC false runs it
// in this process's own rights (already elevated, or a test).
func runElevatedLine(ctx context.Context, root, line string, wait time.Duration, viaUAC bool) elevatedOutput {
	dir, err := os.MkdirTemp("", "agentgo-elev-")
	if err != nil {
		return elevatedOutput{ExitCode: 1, Error: err.Error()}
	}
	defer os.RemoveAll(dir)
	outPath, errPath := filepath.Join(dir, "out.txt"), filepath.Join(dir, "err.txt")

	if root == "" {
		root, _ = os.Getwd()
	}
	code, declined, runErr := runPowerShell(ctx, elevationScript(root, line, outPath, errPath), wait, viaUAC)
	out := elevatedOutput{
		Stdout:   readCaptured(outPath),
		Stderr:   readCaptured(errPath),
		ExitCode: code,
	}
	switch {
	case declined:
		out.ExitCode = 1
		out.Error = "the user declined the Windows UAC prompt (or did not answer it); the command did not run"
	case runErr != nil:
		out.ExitCode = 1
		out.Error = runErr.Error()
	}
	return out
}

// elevationScript is the PowerShell program that runs line with cmd.exe in dir
// and leaves its output in outPath/errPath as UTF-8.
func elevationScript(dir, line, outPath, errPath string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(line))
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"try {",
		"  Set-Location -LiteralPath " + psQuote(dir),
		"  $line = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + b64 + "'))",
		"  $arg = '/S /C \"chcp 65001>nul & ' + $line + '\"'",
		"  $p = Start-Process -FilePath $env:ComSpec -ArgumentList $arg -Wait -PassThru -NoNewWindow" +
			" -RedirectStandardOutput " + psQuote(outPath) + " -RedirectStandardError " + psQuote(errPath),
		"  $null = $p.Handle",
		"  exit $p.ExitCode",
		"} catch {",
		"  [IO.File]::WriteAllText(" + psQuote(errPath) + ", ($_ | Out-String), (New-Object Text.UTF8Encoding $false))",
		"  exit 1",
		"}",
	}, "\n")
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// encodePowerShell is the -EncodedCommand form: base64 of the UTF-16LE text.
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func readCaptured(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimPrefix(string(data), "\ufeff")
	s = strings.ToValidUTF8(s, "?")
	if r := []rune(s); len(r) > elevatedMaxOutputRune {
		s = string(r[:elevatedMaxOutputRune]) + "\n...[output truncated]"
	}
	return s
}

// elevationHint is appended to a failed shell command that looks like a rights
// problem, so the model knows run_elevated exists and when it is the right tool.
func elevationHint(stdout, stderr string, exitCode int) string {
	if exitCode == 0 || !elevationSupported() {
		return ""
	}
	text := strings.ToLower(stdout + "\n" + stderr)
	for _, m := range []string{"access is denied", "拒绝访问", "requires elevation", "需要提升", "administrator privileges", "管理员权限"} {
		if strings.Contains(text, m) {
			return "[hint] the command lacks administrator rights. If it really is needed, ask the user's permission by calling run_elevated for this one command."
		}
	}
	return ""
}
