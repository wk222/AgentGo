package shellcmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// quotedEcho is a command whose meaning depends on its double quotes.
func quotedEcho() string {
	if runtime.GOOS == "windows" {
		return `powershell -NoProfile -Command "Write-Output 'hello world'"`
	}
	return `echo "hello world"`
}

// The line must reach the shell as written. With ordinary arguments Go escapes
// the quotes and cmd.exe echoes the text instead of running it.
func TestQuotedLineRunsAsWritten(t *testing.T) {
	out, err := Command(context.Background(), quotedEcho()).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "hello world") || strings.Contains(got, "Write-Output") {
		t.Fatalf("the line was not run as written: %q", got)
	}
}

func TestLineBeginningWithAQuoteAndPipes(t *testing.T) {
	line := `"` + os.Args[0] + `" -test.run=NoSuchTest_ | findstr /C:"ok" || echo done`
	if runtime.GOOS != "windows" {
		line = `echo "a b" | grep -c "a b"`
	}
	out, err := Command(context.Background(), line).CombinedOutput()
	if err != nil && runtime.GOOS != "windows" {
		t.Fatalf("%v: %s", err, out)
	}
	if len(out) == 0 {
		t.Fatalf("a quoted program path and a pipe produced nothing (err=%v)", err)
	}
}

func waitFor(path string, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// A grandchild writes started.txt at once and marker.txt three seconds later.
func slowGrandchild() string {
	if runtime.GOOS == "windows" {
		return `powershell -NoProfile -Command "Set-Content -Path started.txt -Value x; Start-Sleep 3; Set-Content -Path marker.txt -Value x"`
	}
	return `(touch started.txt; sleep 3; touch marker.txt)`
}

func TestKillTreeEndsWhatTheCommandStarted(t *testing.T) {
	dir := t.TempDir()
	cmd := Command(context.Background(), slowGrandchild())
	cmd.Dir = dir
	Prepare(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()

	if !waitFor(filepath.Join(dir, "started.txt"), 15*time.Second) {
		t.Fatal("the grandchild never started; the test cannot tell anything")
	}
	KillTree(cmd)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not exit after KillTree")
	}
	if waitFor(filepath.Join(dir, "marker.txt"), 5*time.Second) {
		t.Fatal("a process started by the command survived KillTree")
	}
}

func TestKillTreeOnACommandThatNeverStarted(t *testing.T) {
	KillTree(nil)
	KillTree(Command(context.Background(), "echo x")) // Process is nil
}
