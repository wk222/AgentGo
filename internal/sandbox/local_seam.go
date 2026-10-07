package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/shellcmd"
)

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
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	doneStdout := make(chan struct{})
	doneStderr := make(chan struct{})

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				stdoutBuf.Write(chunk)
				if onChunk != nil {
					onChunk("stdout", chunk)
				}
			}
			if err != nil {
				break
			}
		}
		close(doneStdout)
	}()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				stderrBuf.Write(chunk)
				if onChunk != nil {
					onChunk("stderr", chunk)
				}
			}
			if err != nil {
				break
			}
		}
		close(doneStderr)
	}()

	<-doneStdout
	<-doneStderr

	waitErr := cmd.Wait()
	durationMS := time.Since(start).Milliseconds()

	exitCode := 0
	errStr := ""
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
		errStr = waitErr.Error()
	}

	return &CommandResult{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		ExitCode:   exitCode,
		DurationMS: durationMS,
		Error:      errStr,
	}, nil
}
