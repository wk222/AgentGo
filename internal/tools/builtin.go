package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

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
			target := filepath.Join(root, filepath.Clean("/"+strings.ReplaceAll(in.RelativePath, "\\", "/")))
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
	Command string `json:"command" jsonschema:"required,description=Shell command to run in workspace"`
}

type bashOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func registerExecuteBash(r *Registry, workspaceRoot string) error {
	root := workspaceRoot
	t, err := utils.InferTool("execute_bash",
		"Run a shell command in the workspace directory. HIGH RISK — requires user approval.",
		func(ctx context.Context, in bashInput) (bashOutput, error) {
			cmdStr := strings.TrimSpace(in.Command)
			if cmdStr == "" {
				return bashOutput{}, fmt.Errorf("empty command")
			}
			wasInterrupted, _, _ := tool.GetInterruptState[any](ctx)
			if !wasInterrupted {
				return bashOutput{}, tool.Interrupt(ctx, map[string]string{
					"tool": "execute_bash", "command": cmdStr,
				})
			}
			isResume, hasData, approved := tool.GetResumeContext[bool](ctx)
			if isResume && hasData && !approved {
				return bashOutput{Stderr: "user denied", ExitCode: 1}, nil
			}

			// If sandbox docker is active and available, run inside Docker container
			if strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_DOCKER")) == "1" && sandbox.IsDockerAvailable(ctx) {
				image := strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_IMAGE_BASH"))
				if image == "" {
					image = "node:20-alpine"
				}
				stdout, stderr, exitCode, err := sandbox.RunInContainer(ctx, image, root, "/workspace", []string{"sh", "-c", cmdStr}, nil)
				if err != nil {
					return bashOutput{}, err
				}
				return bashOutput{
					Stdout:   stdout,
					Stderr:   stderr,
					ExitCode: exitCode,
				}, nil
			} else if strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_DOCKER")) == "1" {
				fmt.Println("[sandbox] WARNING: Docker Sandbox Mode requested but Docker daemon is unreachable. Falling back to local execution.")
			}

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.Command("cmd", "/C", cmdStr)
			} else {
				cmd = exec.Command("sh", "-c", cmdStr)
			}
			cmd.Dir = root
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				} else {
					return bashOutput{}, err
				}
			}
			return bashOutput{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: code,
			}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
