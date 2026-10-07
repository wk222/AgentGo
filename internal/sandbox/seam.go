package sandbox

import (
	"context"
	"errors"
	"time"
)

// Mode represents the isolation level of the execution seam.
type Mode string

const (
	ModeLocal    Mode = "local"
	ModeLandlock Mode = "landlock"
	ModeDocker   Mode = "docker"
)

var (
	ErrPathEscapesWorkspace = errors.New("sandbox: path escapes allowed workspace directory")
	ErrExecutionDenied      = errors.New("sandbox: command execution blocked by security policy")
)

// CommandRequest defines the command execution parameters.
type CommandRequest struct {
	Command       string            `json:"command"`
	WorkspaceRoot string            `json:"workspace_root"`
	Timeout       time.Duration     `json:"timeout,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	ReadOnlyPaths []string          `json:"read_only_paths,omitempty"`
	ReadWritePaths []string         `json:"read_write_paths,omitempty"`
}

// CommandResult represents the execution outcome.
type CommandResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// StreamHandler receives live output chunks.
type StreamHandler func(kind string, chunk []byte)

// ExecutionSeam abstracts command execution across Local OS, Landlock, and Container environments.
type ExecutionSeam interface {
	Mode() Mode
	Execute(ctx context.Context, req CommandRequest) (*CommandResult, error)
	ExecuteStreaming(ctx context.Context, req CommandRequest, onChunk StreamHandler) (*CommandResult, error)
	ValidatePath(workspaceRoot, targetPath string, writeMode bool) error
}
