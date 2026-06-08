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

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// dynamicInvokeInput accepts arbitrary JSON arguments for compiled dynamic tools.
type dynamicInvokeInput struct {
	Arguments string `json:"arguments" jsonschema:"description=JSON object of tool arguments"`
}

type dynamicInvokeOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// NewPythonDynamicTool materializes a persisted tool as a first-class einotool.BaseTool (C3: tool_search discoverable).
func NewPythonDynamicTool(def DynamicToolDef, sandboxDir string) (einotool.BaseTool, error) {
	name := strings.TrimSpace(def.Name)
	if name == "" {
		return nil, fmt.Errorf("empty dynamic tool name")
	}
	desc := strings.TrimSpace(def.Description)
	if ug := strings.TrimSpace(def.UsageGuide); ug != "" {
		desc = desc + "\n\nWhen to use: " + ug
	}
	defCopy := def
	dir := sandboxDir

	t, err := utils.InferTool(name, desc, func(ctx context.Context, in dynamicInvokeInput) (dynamicInvokeOutput, error) {
		argsJSON := strings.TrimSpace(in.Arguments)
		if argsJSON == "" {
			argsJSON = "{}"
		}
		return runPythonToolBody(ctx, dir, defCopy, argsJSON)
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func runPythonToolBody(ctx context.Context, sandboxDir string, def DynamicToolDef, argsJSON string) (dynamicInvokeOutput, error) {
	scriptPath, err := materializePythonScript(sandboxDir, def)
	if err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}
	argsPath := filepath.Join(sandboxDir, def.Name+"_args.json")
	if err := os.WriteFile(argsPath, []byte(argsJSON), 0o600); err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}

	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	py := "python3"
	if runtime.GOOS == "windows" {
		py = "python"
	}
	wrapper := fmt.Sprintf(`import json, os
args_path = os.environ.get("AGENTGO_ARGS_FILE")
with open(args_path, "r", encoding="utf-8") as f:
    args = json.load(f)
globals_dict = {"args": args, "__name__": "__main__"}
exec(open(%q, encoding="utf-8").read(), globals_dict)
`, scriptPath)
	wrapperPath := filepath.Join(sandboxDir, def.Name+"_wrapper.py")
	_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o600)

	cmd := exec.CommandContext(cctx, py, wrapperPath)
	cmd.Dir = sandboxDir
	cmd.Env = append(os.Environ(), "AGENTGO_ARGS_FILE="+argsPath)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	out := dynamicInvokeOutput{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			out.ExitCode = ee.ExitCode()
		} else {
			out.Error = runErr.Error()
		}
	}
	return out, nil
}

// ToolInfoFromDef builds schema.ToolInfo for capability / UI listing.
func ToolInfoFromDef(def DynamicToolDef) (*schema.ToolInfo, error) {
	t, err := NewPythonDynamicTool(def, "")
	if err != nil {
		return nil, err
	}
	return t.Info(context.Background())
}

// ParseParametersJSON validates parameters JSON when present.
func ParseParametersJSON(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var v any
	return json.Unmarshal([]byte(raw), &v)
}
