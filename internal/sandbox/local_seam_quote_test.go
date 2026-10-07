package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalSeamKeepsDoubleQuotesIntact(t *testing.T) {
	command := `echo "hello world"`
	if runtime.GOOS == "windows" {
		command = `powershell -NoProfile -Command "Write-Output 'hello world'"`
	}
	res, err := NewLocalExecutionSeam().Execute(context.Background(), CommandRequest{
		Command: command, WorkspaceRoot: t.TempDir(), Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello world") || strings.Contains(res.Stdout, "Write-Output") {
		t.Fatalf("command was not run as written: %+v", res)
	}
}
