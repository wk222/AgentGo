package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/shellcmd"
)

// localOutputCap bounds how much of one stream's output is kept: start and end,
// with the gap counted (see shellcmd.Capture).
const localOutputCap = 8 << 20

// LocalExecutionSeam runs commands on the host OS with strict workspace boundary checks.
type LocalExecutionSeam struct{}

// NewLocalExecutionSeam creates a local OS execution seam.
func NewLocalExecutionSeam() *LocalExecutionSeam {
	return &LocalExecutionSeam{}
}

func (s *LocalExecutionSeam) Mode() Mode {
	return ModeLocal
}

func (s *LocalExecutionSeam) ValidatePath(workspaceRoot, targetPath string, writeMode bool) error {
	if workspaceRoot == "" {
		return nil
	}
	cleanRoot := filepath.Clean(workspaceRoot)

	// Check if path is absolute or starts with slash
	isAbsolute := filepath.IsAbs(targetPath) || strings.HasPrefix(targetPath, "/") || strings.HasPrefix(targetPath, "\\")
	var fullPath string
	if isAbsolute {
		fullPath = filepath.Clean(targetPath)
	} else {
		fullPath = filepath.Clean(filepath.Join(cleanRoot, targetPath))
	}

	if fullPath != cleanRoot && !strings.HasPrefix(fullPath, cleanRoot+string(os.PathSeparator)) {
		return fmt.Errorf("%w: %s escapes workspace %s", ErrPathEscapesWorkspace, targetPath, workspaceRoot)
	}
	return nil
}

func (s *LocalExecutionSeam) Execute(ctx context.Context, req CommandRequest) (*CommandResult, error) {
	return s.ExecuteStreaming(ctx, req, nil)
}

// streamWriter hands each piece of a stream to the live handler and keeps a
// bounded copy for the final result.
type streamWriter struct {
	kind    string
	onChunk StreamHandler
	capture *shellcmd.Capture
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if w.onChunk != nil && len(p) > 0 {
		chunk := make([]byte, len(p)) // the handler may keep it
		copy(chunk, p)
		w.onChunk(w.kind, chunk)
	}
	return w.capture.Write(p)
}

func (s *LocalExecutionSeam) ExecuteStreaming(ctx context.Context, req CommandRequest, onChunk StreamHandler) (*CommandResult, error) {
	cmdStr := strings.TrimSpace(req.Command)
	if cmdStr == "" {
		return nil, errors.New("sandbox: command is empty")
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellcmd.Command(execCtx, cmdStr)

	if req.WorkspaceRoot != "" {
		cmd.Dir = filepath.Clean(req.WorkspaceRoot)
	}

	if len(req.Env) > 0 {
		cmd.Env = shellcmd.WithUTF8Env(os.Environ())
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdout := &streamWriter{kind: "stdout", onChunk: onChunk, capture: shellcmd.NewCapture(localOutputCap)}
	stderr := &streamWriter{kind: "stderr", onChunk: onChunk, capture: shellcmd.NewCapture(localOutputCap)}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	// The command and what it starts are one process tree: the timeout or a
	// cancelled context ends all of it, not just the shell, and it runs under
	// the memory and process limits.
	start := time.Now()
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{Detachable: true, BestEffort: true})
	if cmd.Process == nil {
		return nil, err // never started
	}
	durationMS := time.Since(start).Milliseconds()

	errStr := ""
	switch {
	case err != nil:
		code, errStr = 1, err.Error()
	case code != 0:
		errStr = fmt.Sprintf("exit status %d", code)
	}

	return &CommandResult{
		Stdout:     stdout.capture.String(),
		Stderr:     stderr.capture.String(),
		ExitCode:   code,
		DurationMS: durationMS,
		Error:      errStr,
	}, nil
}
