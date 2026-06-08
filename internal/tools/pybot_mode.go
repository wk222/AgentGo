package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"
)

type uvRunInput struct {
	SkillPath  string `json:"skill_path" jsonschema:"description=Directory containing pyproject.toml or main script"`
	Args       string `json:"args" jsonschema:"description=Optional arguments passed to uv run"`
	TimeoutSec int    `json:"timeout_sec" jsonschema:"description=Max seconds, default 120"`
}

type uvRunOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func registerUVRunSkill(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("run_uv_skill",
		"Run a Python skill with `uv run` in the given directory (requires uv on PATH). High-risk: sandbox in production.",
		func(ctx context.Context, in uvRunInput) (uvRunOutput, error) {
			if _, err := exec.LookPath("uv"); err != nil {
				return uvRunOutput{Error: "uv not found on PATH; install https://docs.astral.sh/uv/"}, nil
			}
			dir := strings.TrimSpace(in.SkillPath)
			if dir == "" {
				return uvRunOutput{Error: "skill_path required"}, nil
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workspaceRoot, dir)
			}
			dir = filepath.Clean(dir)
			if !strings.HasPrefix(dir, filepath.Clean(workspaceRoot)) {
				return uvRunOutput{Error: "skill_path must stay under workspace"}, nil
			}
			timeout := time.Duration(in.TimeoutSec) * time.Second
			if timeout <= 0 {
				timeout = 120 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			args := []string{"run"}
			if strings.TrimSpace(in.Args) != "" {
				args = append(args, strings.Fields(in.Args)...)
			}
			cmd := exec.CommandContext(cctx, "uv", args...)
			cmd.Dir = dir
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			runErr := cmd.Run()
			out := uvRunOutput{Stdout: stdout.String(), Stderr: stderr.String()}
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

func registerWorkflowAppTools(r *Registry, dataDir string, onApp func(name, mode, appID string), wfSave func(name, description, nodesJSON string) (string, error)) error {
	type wfIn struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		NodesJSON   string `json:"nodes_json"`
	}
	type wfOut struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	wf, err := utils.InferTool("register_workflow",
		"Register/save a workflow definition (PyFlow nodes). Persists it so it can be executed later via run_workflow.",
		func(_ context.Context, in wfIn) (wfOut, error) {
			id := "wf:" + strings.TrimSpace(in.Name)
			if wfSave != nil {
				wid, err := wfSave(in.Name, in.Description, in.NodesJSON)
				if err == nil && wid != "" {
					id = wid
				}
			}
			_ = os.MkdirAll(filepath.Join(dataDir, "workflows"), 0o755)
			payload, _ := json.Marshal(in)
			_ = os.WriteFile(filepath.Join(dataDir, "workflows", in.Name+".json"), payload, 0o644)
			return wfOut{ID: id, Message: "workflow saved; use run_workflow to execute"}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(wf)

	type appIn struct {
		Name string `json:"name"`
		Mode string `json:"mode" jsonschema:"description=assistant|app_matrix|admin"`
	}
	type appOut struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	app, err := utils.InferTool("register_app",
		"Register an app runtime on the PyBot App Matrix capability bus so app_matrix mode can route to it.",
		func(_ context.Context, in appIn) (appOut, error) {
			id := fmt.Sprintf("app:%s:%s", strings.TrimSpace(in.Mode), strings.TrimSpace(in.Name))
			_ = os.MkdirAll(filepath.Join(dataDir, "apps"), 0o755)
			payload, _ := json.Marshal(in)
			_ = os.WriteFile(filepath.Join(dataDir, "apps", in.Name+".json"), payload, 0o644)
			if onApp != nil {
				onApp(in.Name, in.Mode, id)
			}
			return appOut{ID: id, Message: "app registered on capability bus (app_matrix can subscribe to events)"}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(app)
	return nil
}

// RegisterPyBotModeTools wires creator, list, UV skill, workflow/app stubs (PyBot L1–L5 subset).
func RegisterPyBotModeTools(r *Registry, store *DynamicStore, workspaceRoot, dataDir string, capRegister func(kind, name, scope string), onCompiled func(DynamicToolDef) error, onApp func(name, mode, appID string), wfSave func(name, description, nodesJSON string) (string, error)) error {
	if err := registerCreateTool(r, store, capRegister, onCompiled); err != nil {
		return err
	}
	if err := RegisterCreateToolFromTemplate(r, store, dataDir, capRegister, onCompiled); err != nil {
		return err
	}
	if err := registerListDynamicTools(r, store); err != nil {
		return err
	}
	if err := registerUVRunSkill(r, workspaceRoot); err != nil {
		return err
	}
	return registerWorkflowAppTools(r, dataDir, onApp, wfSave)
}
