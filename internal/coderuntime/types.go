package coderuntime

import (
	"context"
	"time"
)

// Language represents the target script language for Code Mode.
type Language string

const (
	LangPython Language = "python"
	LangShell  Language = "shell"
	LangJS     Language = "javascript"
)

// ExecutionRequest represents a multi-tool or computational script execution request.
type ExecutionRequest struct {
	Language Language          `json:"language"`
	Code     string            `json:"code"`
	Timeout  time.Duration     `json:"timeout,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// ExecutionResult captures stdout, stderr, return value, and timing.
type ExecutionResult struct {
	Stdout     string        `json:"stdout"`
	Stderr     string        `json:"stderr"`
	ExitCode   int           `json:"exit_code"`
	DurationMS int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
}

// Runtime executes user/model scripts in a controlled environment.
type Runtime interface {
	Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)
}
