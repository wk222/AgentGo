package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestElevationScriptCarriesTheCommandIntact(t *testing.T) {
	line := `echo "it's 100% 中文" & dir "C:\Program Files"`
	script := elevationScript(`C:\Users\o'neil\work`, line, `C:\t\out.txt`, `C:\t\err.txt`)
	// The command is only ever data inside the script: base64, never spliced as code.
	if strings.Contains(script, "Program Files") || strings.Contains(script, "echo") {
		t.Fatalf("command text leaked into the script body:\n%s", script)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(line))) {
		t.Fatal("script does not carry the command")
	}
	if !strings.Contains(script, `'C:\Users\o''neil\work'`) {
		t.Fatalf("single quote in a path must be doubled:\n%s", script)
	}
	// -EncodedCommand round-trips as UTF-16LE.
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell(script))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("encoded command: %v", err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if string(utf16.Decode(u)) != script {
		t.Fatal("encoded command does not decode back to the script")
	}
}

func TestElevationHintOnlyForRightsProblems(t *testing.T) {
	if !elevationSupported() {
		t.Skip("elevation is a Windows feature")
	}
	if elevationHint("", "拒绝访问。", 5) == "" {
		t.Error("access denied should point at run_elevated")
	}
	if elevationHint("", "Access is denied.", 1) == "" {
		t.Error("access denied (English) should point at run_elevated")
	}
	if elevationHint("", "file not found", 1) != "" {
		t.Error("an ordinary failure must not suggest elevation")
	}
	if elevationHint("", "Access is denied.", 0) != "" {
		t.Error("a command that succeeded needs no hint")
	}
}

func TestRunElevatedRejectsEmptyAndOversizedCommands(t *testing.T) {
	if got := runElevatedCommand(context.Background(), t.TempDir(), elevatedInput{Command: "  "}); got.Error == "" {
		t.Error("an empty command must be refused")
	}
	long := elevatedInput{Command: strings.Repeat("a", elevatedMaxCommand+1)}
	if got := runElevatedCommand(context.Background(), t.TempDir(), long); got.Error == "" {
		t.Error("an oversized command must be refused")
	}
}

// The same script, run in this process's own rights (no UAC): output capture,
// exit code, working directory and non-ASCII text must all come through.
func TestElevationScriptCapturesOutputAndExitCode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs cmd.exe and PowerShell")
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "中文名.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runElevatedLine(context.Background(), ws, `cd & dir /b & echo 出错了 1>&2 & exit /b 7`, 60*time.Second, false)
	if out.Error != "" {
		t.Fatalf("unexpected error: %s", out.Error)
	}
	if out.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", out.ExitCode)
	}
	if !strings.Contains(out.Stdout, filepath.Base(ws)) || !strings.Contains(out.Stdout, "中文名.txt") {
		t.Errorf("stdout should show the workspace as cwd and its files: %q", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "出错了") {
		t.Errorf("stderr lost the Chinese text: %q", out.Stderr)
	}
}

// Live: really goes through UAC. It elevates, so it only runs when asked for
// (and on a PC whose UAC policy the tester is happy with).
func TestRunElevatedThroughUAC(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_UAC") != "1" {
		t.Skip("set AGENTGO_TEST_UAC=1 to run (triggers a real elevation)")
	}
	out := runElevatedLine(context.Background(), t.TempDir(), `net session >nul 2>&1 && echo ADMIN || echo NOT-ADMIN`, 90*time.Second, true)
	if out.Error != "" {
		t.Fatalf("elevated run failed: %s", out.Error)
	}
	if !strings.Contains(out.Stdout, "ADMIN") || strings.Contains(out.Stdout, "NOT-ADMIN") {
		t.Fatalf("the command did not run with administrator rights: %+v", out)
	}
}
