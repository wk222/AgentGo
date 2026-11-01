package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/config"
	"agentgo/internal/sandbox"
)

type getTimeInput struct{}

type getTimeOutput struct {
	Now string `json:"now"`
}

func registerGetTime(r *Registry) error {
	t, err := utils.InferTool("get_current_time",
		"Return the current local date and time in RFC3339 format.",
		func(_ context.Context, _ getTimeInput) (getTimeOutput, error) {
			return getTimeOutput{Now: time.Now().Format(time.RFC3339)}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type listDirInput struct {
	RelativePath string `json:"relative_path" jsonschema:"description=Path relative to workspace root, empty for root"`
}

type listDirOutput struct {
	Entries []string `json:"entries"`
}

func registerListWorkspace(r *Registry, workspaceRoot string) error {
	root := workspaceRoot
	t, err := utils.InferTool("list_workspace_dir",
		"List files and directories under the AgentGo workspace root. Pass relative_path for subfolders.",
		func(_ context.Context, in listDirInput) (listDirOutput, error) {
			_, target, err := resolveWorkspacePath(root, in.RelativePath, true, true)
			if err != nil {
				return listDirOutput{}, err
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				return listDirOutput{}, err
			}
			var names []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") && e.Name() != ".cursor" {
					continue
				}
				prefix := "file:"
				if e.IsDir() {
					prefix = "dir:"
				}
				names = append(names, prefix+e.Name())
			}
			return listDirOutput{Entries: names}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type echoInput struct {
	Message string `json:"message" jsonschema:"required"`
}

type echoOutput struct {
	Echo string `json:"echo"`
}

func registerEcho(r *Registry) error {
	t, err := utils.InferTool("echo_message", "Echo back a short message (connectivity test).",
		func(_ context.Context, in echoInput) (echoOutput, error) {
			return echoOutput{Echo: strings.TrimSpace(in.Message)}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type bashInput struct {
	Command        string `json:"command" jsonschema:"required,description=Shell command to run in workspace"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"description=Optional time limit for this command in seconds. The maximum is 1800."`
}

type bashOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func registerExecuteBash(r *Registry, workspaceRoot string) error {
	root := workspaceRoot
	t, err := utils.InferTool("execute_bash",
		"Run a shell command in the workspace directory. HIGH RISK — requires user approval. "+shellTimeoutNote(),
		func(ctx context.Context, in bashInput) (bashOutput, error) {
			cmdStr := strings.TrimSpace(in.Command)
			if cmdStr == "" {
				return bashOutput{}, fmt.Errorf("empty command")
			}
			// Approval is the governance middleware's job (execute_bash is in its
			// always-approve list; a session that waives approval, such as the
			// reverse harness, is let through by policy). This tool used to add its
			// own tool.Interrupt on top: when policy had already waived approval it
			// still paused, with an interrupt id that is not in the approval queue,
			// so the pause could never be approved ("not found or already
			// resolved") and every shell command in those sessions failed.

			// If sandbox docker is active and available, run inside Docker container
			if config.SandboxDocker.On() && sandbox.IsDockerAvailable(ctx) {
				image := config.SandboxImageBash.Get()
				if image == "" {
					image = "node:20-alpine"
				}
				stdout, stderr, exitCode, err := sandbox.RunInContainer(ctx, image, root, "/workspace", []string{"sh", "-c", cmdStr}, nil)
				if err != nil {
					return bashOutput{}, err
				}
				return bashOutput{
					Stdout:   stripANSIString(stdout),
					Stderr:   stripANSIString(stderr),
					ExitCode: exitCode,
				}, nil
			} else if config.SandboxDocker.On() {
				fmt.Println("[sandbox] WARNING: Docker Sandbox Mode requested but Docker daemon is unreachable. Falling back to local execution.")
			}

			return cleanBashOutput(runShell(ctx, root, cmdStr, shellTimeout(in.TimeoutSeconds)))
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
