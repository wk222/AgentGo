package terminal

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestRunKeepsDoubleQuotesIntact(t *testing.T) {
	command := `echo "hello world"`
	if runtime.GOOS == "windows" {
		command = `powershell -NoProfile -Command "Write-Output 'hello world'"`
	}
	res, err := Run(context.Background(), t.TempDir(), command)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello world") || strings.Contains(res.Stdout, "Write-Output") {
		t.Fatalf("command was not run as written: %+v", res)
	}
}
