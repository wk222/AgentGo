package coderuntime

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type runScriptInput struct {
	Language  string `json:"language" jsonschema:"description=Script language: python (default), shell, or javascript"`
	Code      string `json:"code" jsonschema:"required,description=The code or script to execute in workspace"`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Execution timeout in milliseconds (default 60000)"`
}

type runScriptOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// NewRunScriptTool creates an invokable tool for multi-step script and code execution in workspace.
func NewRunScriptTool(rt Runtime) (tool.InvokableTool, error) {
	if rt == nil {
		return nil, fmt.Errorf("coderuntime: runtime is required")
	}

	return utils.InferTool(
		"run_batch_script",
		"Execute a multi-step batch script (Python, Shell, or JavaScript) in the workspace. Use this to perform bulk data processing, multi-file inspection, or complex algorithms in a single step.",
		func(ctx context.Context, in runScriptInput) (runScriptOutput, error) {
			timeout := 60 * time.Second
			if in.TimeoutMS > 0 {
				timeout = time.Duration(in.TimeoutMS) * time.Millisecond
			}

			lang := Language(in.Language)
			if lang == "" {
				lang = LangPython
			}

			res, err := rt.Execute(ctx, ExecutionRequest{
				Language: lang,
				Code:     in.Code,
				Timeout:  timeout,
			})
			if err != nil {
				return runScriptOutput{Error: err.Error()}, nil
			}

			return runScriptOutput{
				Stdout:     res.Stdout,
				Stderr:     res.Stderr,
				ExitCode:   res.ExitCode,
				DurationMS: res.DurationMS,
				Error:      res.Error,
			}, nil
		},
	)
}
