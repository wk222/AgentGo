package terminal

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Result is returned to the desktop terminal panel.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

// Run executes a command in the workspace directory for the desktop terminal panel.
func Run(ctx context.Context, workspaceRoot, command string) (Result, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return Result{}, nil
	}
	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = workspaceRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return Result{}, err
		}
	}
	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
		Duration: time.Since(start).String(),
	}, nil
}
