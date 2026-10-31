package agentpack

import (
	"agentgo/internal/skills"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/components/tool/utils"
	"strings"
)

type VersionRequest struct {
	Action    string   `json:"action" jsonschema:"description=list, snapshot, restore, or validate"`
	ID        string   `json:"id,omitempty"`
	Title     string   `json:"title,omitempty"`
	Workflows []string `json:"workflows,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	InnerApps []string `json:"innerapps,omitempty"`
	Skills    []string `json:"skills,omitempty"`
}

func (e *Engine) VersionCommand(ctx context.Context, in VersionRequest) map[string]any {
	result := map[string]any{"success": true}
	var err error
	switch strings.ToLower(in.Action) {
	case "list":
		result["versions"], err = e.Versions()
	case "snapshot":
		result["version"], err = e.Snapshot(ctx, ExportRequest{Title: in.Title, Workflows: in.Workflows, Tools: in.Tools, InnerApps: in.InnerApps, Skills: in.Skills, IncludeDependencies: true})
	case "restore":
		result["result"], err = e.Restore(ctx, in.ID)
	case "validate":
		var p string
		p, err = e.versionPath(in.ID)
		if err == nil {
			report := Validate(p)
			result["validation"] = report
			result["success"] = report.OK
		}
	default:
		err = fmt.Errorf("unknown action; use list, snapshot, restore or validate")
	}
	if err != nil {
		result["success"] = false
		result["error"] = err.Error()
	}
	return result
}
func RegisterEvolutionTools(reg *tools.Registry, e *Engine) error {
	versionTool, err := utils.InferTool("manage_extension_versions",
		"Manage local versions of generated tools, workflows, inner apps (including UI pages and agent profiles), and skills. Snapshot EXISTING artifacts before changing them, snapshot a verified result after completion. restore creates a backup before replacing current contents. validate checks structure, not runtime behavior.",
		func(ctx context.Context, in VersionRequest) (map[string]any, error) {
			return e.VersionCommand(ctx, in), nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(versionTool)
	save, err := utils.InferTool("save_workflow_definition",
		"Save and hot-register a complete workflow definition with explicit id, name, nodes and edges. Preserves arbitrary node ids and branches. Automatically snapshots an existing definition before replacement. Checks graph structure; run_workflow separately to test execution.",
		func(ctx context.Context, in struct {
			Definition workflow.Definition `json:"definition"`
		}) (map[string]any, error) {
			d := in.Definition
			if err := ValidateWorkflow(d); err != nil {
				return map[string]any{"success": false, "error": err.Error()}, nil
			}
			if e.WF == nil {
				return map[string]any{"success": false, "error": "workflow store unavailable"}, nil
			}
			if _, err := e.WF.Get(d.ID); err == nil {
				if _, err := e.Snapshot(ctx, ExportRequest{Title: "修改前：" + d.Name, Workflows: []string{d.ID}}); err != nil {
					return map[string]any{"success": false, "error": err.Error()}, nil
				}
			}
			if err := e.WF.Save(d); err != nil {
				return map[string]any{"success": false, "error": err.Error()}, nil
			}
			if e.OnReload != nil {
				if err := e.OnReload(); err != nil {
					return map[string]any{"success": false, "error": "saved but reload failed: " + err.Error()}, nil
				}
			}
			return map[string]any{"success": true, "id": d.ID, "validation": "structure only; execute run_workflow to test"}, nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(save)
	catalog, err := utils.InferTool("extension_catalog",
		"List generated capabilities and the tools available for extending AgentGo without modifying its Eino runtime. Use create_tool, scaffold_inner_app/update_inner_app_file/verify_inner_app, save_workflow_definition, evolve_skill, and manage_extension_versions. Native Go changes require rebuilding.",
		func(ctx context.Context, in struct{}) (string, error) {
			data, err := e.Catalog(ctx)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(data)
			return string(b), nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(catalog)
	return nil
}
func (e *Engine) Catalog(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	if e.Dyn != nil {
		items, err := e.Dyn.List()
		if err != nil {
			return nil, err
		}
		list := []map[string]string{}
		for _, d := range items {
			list = append(list, map[string]string{"name": d.Name, "description": d.Description})
		}
		out["tools"] = list
	}
	if e.WF != nil {
		items, err := e.WF.List()
		if err != nil {
			return nil, err
		}
		list := []map[string]string{}
		for _, d := range items {
			list = append(list, map[string]string{"id": d.ID, "name": d.Name, "description": d.Description})
		}
		out["workflows"] = list
	}
	if e.Apps != nil {
		items, err := e.Apps.List(ctx, 1000)
		if err != nil {
			return nil, err
		}
		list := []map[string]string{}
		for _, d := range items {
			list = append(list, map[string]string{"name": d.Name, "kind": d.Kind, "description": d.Description})
		}
		out["apps"] = list
	}
	loader := skills.NewLoader(e.Workspace)
	skillItems := []map[string]string{}
	for _, d := range loader.Reload() {
		skillItems = append(skillItems, map[string]string{"name": d.Name, "description": d.Description})
	}
	out["skills"] = skillItems
	out["runtime_boundary"] = "Generated Python tools, UI apps, agent apps, workflows and skills are hot-loaded. Eino and native Go code require a rebuild."
	return out, nil
}
