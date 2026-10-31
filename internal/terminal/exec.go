package terminal

import (
	"context"
	"strings"
	"time"

	"agentgo/internal/shellcmd"
)

// outputCap bounds the output of one command that is held in memory, per
// stream: the start and the end are kept and the gap is counted.
const outputCap = 8 << 20

// Result is returned to the desktop terminal panel.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

// Run executes a command in the workspace directory for the desktop terminal panel.
// The command and what it starts are one process tree: when ctx ends, all of it
// ends. What a command leaves running on purpose (an application it opened) is
// left alone once it finishes.
func Run(ctx context.Context, workspaceRoot, command string) (Result, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return Result{}, nil
	}
	start := time.Now()
	cmd := shellcmd.Command(ctx, command)
	cmd.Dir = workspaceRoot
	stdout, stderr := shellcmd.NewCapture(outputCap), shellcmd.NewCapture(outputCap)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{Detachable: true, BestEffort: true})
	if err != nil {
		return Result{}, err
	}
	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
		Duration: time.Since(start).String(),
	}, nil
}
