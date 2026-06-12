package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/apps"
)

// InnerAppScaffold wires PyBot-style create_app / update_app_file tools.
type InnerAppScaffold struct {
	Scaffolder *apps.Scaffolder
	OnUpsert   func(apps.InnerApp)
}

func RegisterInnerAppScaffoldTools(r *Registry, sc *InnerAppScaffold) error {
	if sc == nil || sc.Scaffolder == nil || r == nil {
		return nil
	}

	scaffoldTool, err := utils.InferTool("scaffold_inner_app",
		`Create a managed inner app with UI scaffold (PyBot create_app). Writes data/apps/<name>/ with index.html, static/, app.json, registers DB.
Modes: chat (chat UI + agentGo.chat), static (ping demo), workflow (requires workflow_id, run button calls workflow_run).`,
		func(_ context.Context, in struct {
			Name         string `json:"name"`
			DisplayName  string `json:"display_name,omitempty"`
			Description  string `json:"description,omitempty"`
			Mode         string `json:"mode" jsonschema:"description=chat, static, or workflow"`
			WorkflowID   string `json:"workflow_id,omitempty"`
			SystemPrompt string `json:"system_prompt,omitempty"`
			Exports      string `json:"exports,omitempty" jsonschema:"description=comma-separated capability names"`
			Overwrite    bool   `json:"overwrite,omitempty"`
		}) (string, error) {
			var exports []string
			for _, e := range strings.Split(in.Exports, ",") {
				if t := strings.TrimSpace(e); t != "" {
					exports = append(exports, t)
				}
			}
			res, err := sc.Scaffolder.Scaffold(context.Background(), apps.ScaffoldOptions{
				Name: in.Name, DisplayName: in.DisplayName, Description: in.Description,
				Mode: in.Mode, WorkflowID: in.WorkflowID, SystemPrompt: in.SystemPrompt,
				Exports: exports, Overwrite: in.Overwrite,
			})
			if err != nil && !res.Success {
				b, _ := json.Marshal(res)
				return string(b), nil
			}
			if res.Success && sc.OnUpsert != nil && sc.Scaffolder.Store != nil {
				if app, err2 := sc.Scaffolder.Store.GetByName(context.Background(), res.AppName); err2 == nil {
					sc.OnUpsert(app)
				}
			}
			b, _ := json.Marshal(res)
			return string(b), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(scaffoldTool)

	updateTool, err := utils.InferTool("update_inner_app_file",
		`Update a file inside a scaffolded inner app (index.html, static/app.js, static/style.css, app.json).
index.html must include /agentgo-app-helpers.js before static/app.js. Validation issues are returned in the response.`,
		func(_ context.Context, in struct {
			AppName  string `json:"app_name"`
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}) (string, error) {
			res, err := sc.Scaffolder.UpdateFile(context.Background(), in.AppName, in.FilePath, in.Content)
			b, _ := json.Marshal(res)
			if err != nil && !res.Success {
				return string(b), nil
			}
			return string(b), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(updateTool)

	readTool, err := utils.InferTool("read_inner_app_file",
		"Read a text file from an inner app bundle.",
		func(_ context.Context, in struct {
			AppName  string `json:"app_name"`
			FilePath string `json:"file_path"`
		}) (string, error) {
			content, err := sc.Scaffolder.ReadFile(in.AppName, in.FilePath)
			out := map[string]any{"success": err == nil, "content": content}
			if err != nil {
				out["error"] = err.Error()
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(readTool)

	listFiles, err := utils.InferTool("list_inner_app_files",
		"List relative file paths in an inner app bundle directory.",
		func(_ context.Context, in struct {
			AppName string `json:"app_name"`
			Limit   int    `json:"limit,omitempty"`
		}) (string, error) {
			files, err := sc.Scaffolder.ListFiles(in.AppName, in.Limit)
			out := map[string]any{"success": err == nil, "files": files}
			if err != nil {
				out["error"] = err.Error()
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(listFiles)

	return nil
}
