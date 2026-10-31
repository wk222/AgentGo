package tools

import (
	"agentgo/internal/processutil"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/sandbox"

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

// pythonInterpreter locates the interpreter dynamic Python tools run under.
func pythonInterpreter() (string, error) {
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python interpreter not found (looked for %v)", names)
}

// ValidatePythonSyntax parses code with the interpreter's ast module. The code
// is parsed, never executed: a tool that cannot even parse must not be
// reported as compiled, registered, or persisted.
func ValidatePythonSyntax(ctx context.Context, code string) error {
	if strings.TrimSpace(code) == "" {
		return nil // empty body materializes as the built-in stub
	}
	py, err := pythonInterpreter()
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Isolated startup must not execute workspace/PYTHONPATH site hooks or
	// inherit undeclared credentials, even when the runtime uses Docker.
	cmd := exec.CommandContext(cctx, py, "-I", "-S", "-c", `import sys
try:
    compile(sys.stdin.read(), "<dynamic-tool>", "exec")
except SyntaxError as e:
    print(f"line {e.lineno}: {e.msg}", file=sys.stderr)
    sys.exit(1)
`)
	processutil.HideWindow(cmd)
	cmd.Env = buildToolEnv(DynamicToolDef{})
	cmd.Stdin = strings.NewReader(code)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("python syntax invalid: %s", msg)
	}
	return nil
}

// ValidateDynamicToolDef checks everything that can be known before a tool is
// persisted or registered: parameter JSON parses, the Python body parses, and
// the tool constructs with a valid name. Callers must run this BEFORE saving
// so "stored" and "runnable" never diverge.
func ValidateDynamicToolDef(ctx context.Context, def DynamicToolDef) error {
	if err := ParseParametersJSON(def.Parameters); err != nil {
		return fmt.Errorf("parameters: %w", err)
	}
	for _, n := range def.EnvVars {
		if !envNamePattern.MatchString(n) {
			return fmt.Errorf("invalid environment variable name %q", n)
		}
	}
	if err := ValidatePythonSyntax(ctx, def.Code); err != nil {
		return err
	}
	t, err := NewPythonDynamicTool(def, "")
	if err != nil {
		return err
	}
	info, err := t.Info(ctx)
	if err != nil || info == nil {
		return fmt.Errorf("tool info: %w", err)
	}
	if !ValidToolName(info.Name) {
		return fmt.Errorf("invalid tool name %q: use only letters, digits, '_', '.', '-' (max 64 chars)", info.Name)
	}
	return nil
}

// toolEnvBaseAllow lists host variables a generated tool always receives:
// interpreter/runtime plumbing, never credentials. Anything else — above all
// secret-looking names — is withheld unless the tool declares it in EnvVars
// at creation time (create_tool is approval-gated, so the declaration is a
// deliberate, reviewable act).
var toolEnvBaseAllow = map[string]bool{
	"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
	"COMSPEC": true, "TEMP": true, "TMP": true, "TMPDIR": true,
	"HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"APPDATA": true, "LOCALAPPDATA": true, "LANG": true, "LANGUAGE": true,
	"LC_ALL": true, "TZ": true, "NUMBER_OF_PROCESSORS": true,
	"PROCESSOR_ARCHITECTURE": true, "OS": true, "USERNAME": true,
	"PYTHONPATH": true, "PYTHONIOENCODING": true, "PYTHONUTF8": true,
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseEnvVars parses the comma-separated env declaration from create_tool.
func ParseEnvVars(raw string) ([]string, error) {
	var out []string
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !envNamePattern.MatchString(n) {
			return nil, fmt.Errorf("invalid environment variable name %q", n)
		}
		out = append(out, n)
	}
	return out, nil
}

// buildToolEnv builds the environment for a tool process: base allowlist plus
// explicitly declared variables, plus caller-supplied extras. Host secrets
// (API keys, tokens, private session tokens, ...) do not leak into
// generated code by default.
func buildToolEnv(def DynamicToolDef, extra ...string) []string {
	env := make([]string, 0, 32)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if toolEnvBaseAllow[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	for _, name := range def.EnvVars {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return append(env, extra...)
}

// declaredEnvOnly returns just the declared variables (for containers, which
// provide their own base environment).
func declaredEnvOnly(def DynamicToolDef, extra ...string) []string {
	env := make([]string, 0, len(def.EnvVars)+len(extra))
	for _, name := range def.EnvVars {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return append(env, extra...)
}

func runPythonToolBody(ctx context.Context, sandboxDir string, def DynamicToolDef, argsJSON string) (dynamicInvokeOutput, error) {
	// Per-invocation directory: args and wrapper files used to be named
	// <tool>_args.json / <tool>_wrapper.py directly in the sandbox, so two
	// concurrent calls to the same tool overwrote each other's arguments.
	runDir, err := os.MkdirTemp(sandboxDir, def.Name+"-run-")
	if err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}
	defer os.RemoveAll(runDir)
	scriptPath, err := materializePythonScript(runDir, def)
	if err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}
	argsPath := filepath.Join(runDir, "args.json")
	if err := os.WriteFile(argsPath, []byte(argsJSON), 0o600); err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}

	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// Real isolation when the operator opted in and Docker is reachable: the
	// tool runs in a container with only its declared env vars. This is the
	// same path execute_dynamic_tool uses; generated tools share it so the
	// sandbox setting cannot be bypassed by picking the newer entry point.
	if config.SandboxDocker.On() && sandbox.IsDockerAvailable(cctx) {
		image := config.SandboxImagePython.Get()
		if image == "" {
			image = "python:3.10-slim"
		}
		runRel := filepath.Base(runDir)
		wrapper := fmt.Sprintf(`import json, os
args_path = os.environ.get("AGENTGO_ARGS_FILE")
with open(args_path, "r", encoding="utf-8") as f:
    args = json.load(f)
globals_dict = {"args": args, "__name__": "__main__"}
exec(open("/sandbox/%s/%s.py", encoding="utf-8").read(), globals_dict)
`, runRel, def.Name)
		wrapperPath := filepath.Join(runDir, "wrapper.py")
		_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o600)
		stdout, stderr, exitCode, runErr := sandbox.RunInContainer(cctx, image, sandboxDir, "/sandbox",
			[]string{"python", "/sandbox/" + runRel + "/wrapper.py"},
			declaredEnvOnly(def, "AGENTGO_ARGS_FILE=/sandbox/"+runRel+"/args.json"),
		)
		out := dynamicInvokeOutput{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
		if runErr != nil {
			out.Error = runErr.Error()
		}
		return out, nil
	} else if config.SandboxDocker.On() {
		log.Printf("[sandbox] Docker sandbox requested but daemon unreachable; running %s locally with filtered env", def.Name)
	}

	py, err := pythonInterpreter()
	if err != nil {
		return dynamicInvokeOutput{Error: err.Error()}, nil
	}
	wrapper := fmt.Sprintf(`import json, os
args_path = os.environ.get("AGENTGO_ARGS_FILE")
with open(args_path, "r", encoding="utf-8") as f:
    args = json.load(f)
globals_dict = {"args": args, "__name__": "__main__"}
exec(open(%q, encoding="utf-8").read(), globals_dict)
`, scriptPath)
	wrapperPath := filepath.Join(runDir, "wrapper.py")
	_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o600)

	cmd := exec.CommandContext(cctx, py, wrapperPath)
	processutil.HideWindow(cmd)
	cmd.Dir = sandboxDir // keep cwd stable: existing tools may open paths relative to the sandbox
	cmd.Env = buildToolEnv(def, "AGENTGO_ARGS_FILE="+argsPath)
	var stdout, stderr processutil.OutputBuffer
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
