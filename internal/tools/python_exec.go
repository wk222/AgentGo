package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/sandbox"
)

type execDynamicInput struct {
	ToolName string `json:"tool_name"`
	ArgsJSON string `json:"args_json" jsonschema:"description=JSON object of arguments"`
}

type execDynamicOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// RegisterDynamicPythonExec adds legacy execute_dynamic_tool (generic dispatcher). Prefer per-tool compile via SyncDynamicFromStore.
func RegisterDynamicPythonExec(r *Registry, store *DynamicStore, sandboxDir string) error {
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return err
	}
	t, err := utils.InferTool("execute_dynamic_tool",
		"Run a user-created dynamic tool by name. Executes stored Python in an isolated subprocess under the sandbox directory.",
		func(ctx context.Context, in execDynamicInput) (execDynamicOutput, error) {
			if store == nil {
				return execDynamicOutput{Error: "dynamic store unavailable"}, nil
			}
			def, err := store.Get(strings.TrimSpace(in.ToolName))
			if err != nil {
				return execDynamicOutput{Error: err.Error()}, nil
			}
			scriptPath, err := materializePythonScript(sandboxDir, def)
			if err != nil {
				return execDynamicOutput{Error: err.Error()}, nil
			}
			argsPath := filepath.Join(sandboxDir, def.Name+"_args.json")
			if in.ArgsJSON == "" {
				in.ArgsJSON = "{}"
			}
			if err := os.WriteFile(argsPath, []byte(in.ArgsJSON), 0o600); err != nil {
				return execDynamicOutput{Error: err.Error()}, nil
			}

			cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()

			// If sandbox docker is active and available, run inside Docker container
			if strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_DOCKER")) == "1" && sandbox.IsDockerAvailable(ctx) {
				image := strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_IMAGE_PYTHON"))
				if image == "" {
					image = "python:3.10-slim"
				}
				wrapper := fmt.Sprintf(`import json, os, runpy
args_path = os.environ.get("AGENTGO_ARGS_FILE")
with open(args_path, "r", encoding="utf-8") as f:
    args = json.load(f)
globals_dict = {"args": args, "__name__": "__main__"}
exec(open("/sandbox/%s.py", encoding="utf-8").read(), globals_dict)
`, def.Name)
				wrapperPath := filepath.Join(sandboxDir, def.Name+"_wrapper.py")
				_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o600)

				stdout, stderr, exitCode, err := sandbox.RunInContainer(cctx, image, sandboxDir, "/sandbox",
					[]string{"python", "/sandbox/" + def.Name + "_wrapper.py"},
					[]string{"AGENTGO_ARGS_FILE=/sandbox/" + def.Name + "_args.json"},
				)
				out := execDynamicOutput{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
				if err != nil {
					out.Error = err.Error()
				}
				return out, nil
			} else if strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_DOCKER")) == "1" {
				fmt.Println("[sandbox] WARNING: Docker Sandbox Mode requested but Docker daemon is unreachable. Falling back to local execution.")
			}

			var cmd *exec.Cmd
			py := "python3"
			if runtime.GOOS == "windows" {
				py = "python"
			}
			// Wrapper passes AGENTGO_ARGS_FILE to user script
			wrapper := fmt.Sprintf(`import json, os, runpy
args_path = os.environ.get("AGENTGO_ARGS_FILE")
with open(args_path, "r", encoding="utf-8") as f:
    args = json.load(f)
globals_dict = {"args": args, "__name__": "__main__"}
exec(open(%q, encoding="utf-8").read(), globals_dict)
`, scriptPath)
			wrapperPath := filepath.Join(sandboxDir, def.Name+"_wrapper.py")
			_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o600)

			cmd = exec.CommandContext(cctx, py, wrapperPath)
			cmd.Dir = sandboxDir
			cmd.Env = append(os.Environ(), "AGENTGO_ARGS_FILE="+argsPath)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			runErr := cmd.Run()
			out := execDynamicOutput{Stdout: stdout.String(), Stderr: stderr.String()}
			if runErr != nil {
				if ee, ok := runErr.(*exec.ExitError); ok {
					out.ExitCode = ee.ExitCode()
				} else {
					out.Error = runErr.Error()
				}
			}
			return out, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func materializePythonScript(sandboxDir string, def DynamicToolDef) (string, error) {
	code := strings.TrimSpace(def.Code)
	if code == "" {
		code = `import json
print(json.dumps({"ok": True, "message": "empty tool body", "args": args}))`
	}
	path := filepath.Join(sandboxDir, def.Name+".py")
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// SummarizeArgsForLog returns safe short args string.
func SummarizeArgsForLog(argsJSON string) string {
	var m map[string]any
	if json.Unmarshal([]byte(argsJSON), &m) == nil && len(m) < 8 {
		b, _ := json.Marshal(m)
		if len(b) < 200 {
			return string(b)
		}
	}
	if len(argsJSON) > 120 {
		return argsJSON[:120] + "…"
	}
	return argsJSON
}
