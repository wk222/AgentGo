package coderuntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LocalProcessRuntime executes scripts using available local interpreters (Python, Bash/cmd).
type LocalProcessRuntime struct {
	workspaceRoot string
	tempDir       string
}

// NewLocalProcessRuntime creates a code runtime executor bound to workspaceRoot.
func NewLocalProcessRuntime(workspaceRoot string) (*LocalProcessRuntime, error) {
	root := filepath.Clean(workspaceRoot)
	tmp := filepath.Join(root, ".agentgo_tmp")
	_ = os.MkdirAll(tmp, 0o755)
	return &LocalProcessRuntime{
		workspaceRoot: root,
		tempDir:       tmp,
	}, nil
}

func (r *LocalProcessRuntime) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, errors.New("coderuntime: empty code provided")
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	scriptID := "script_" + uuid.NewString()[:8]
	var cmd *exec.Cmd

	switch req.Language {
	case LangPython, "":
		scriptPath := filepath.Join(r.tempDir, scriptID+".py")
		if err := os.WriteFile(scriptPath, []byte(code), 0o644); err != nil {
			return nil, fmt.Errorf("coderuntime: write python script failed: %w", err)
		}
		defer func() { _ = os.Remove(scriptPath) }()

		pythonBin := findPythonBin()
		cmd = exec.CommandContext(execCtx, pythonBin, scriptPath)

	case LangShell:
		if runtime.GOOS == "windows" {
			scriptPath := filepath.Join(r.tempDir, scriptID+".bat")
			if err := os.WriteFile(scriptPath, []byte(code), 0o644); err != nil {
				return nil, fmt.Errorf("coderuntime: write batch script failed: %w", err)
			}
			defer func() { _ = os.Remove(scriptPath) }()
			cmd = exec.CommandContext(execCtx, "cmd", "/C", scriptPath)
		} else {
			cmd = exec.CommandContext(execCtx, "sh", "-c", code)
		}

	case LangJS:
		scriptPath := filepath.Join(r.tempDir, scriptID+".js")
		if err := os.WriteFile(scriptPath, []byte(code), 0o644); err != nil {
			return nil, fmt.Errorf("coderuntime: write js script failed: %w", err)
		}
		defer func() { _ = os.Remove(scriptPath) }()
		cmd = exec.CommandContext(execCtx, "node", scriptPath)

	default:
		return nil, fmt.Errorf("coderuntime: unsupported language %q", req.Language)
	}

	cmd.Dir = r.workspaceRoot
	if len(req.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	exitCode := 0
	errStr := ""
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
		errStr = err.Error()
	}

	return &ExecutionResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitCode:   exitCode,
		DurationMS: durationMS,
		Error:      errStr,
	}, nil
}

func findPythonBin() string {
	for _, bin := range []string{"python", "python3", "py"} {
		if p, err := exec.LookPath(bin); err == nil && p != "" {
			return bin
		}
	}
	return "python"
}
