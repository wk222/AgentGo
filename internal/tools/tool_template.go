package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
)

type createFromTemplateInput struct {
	Template string `json:"template" jsonschema:"description=Template file name without .json, under data/tool_templates"`
	ToolName string `json:"tool_name" jsonschema:"description=Optional override snake_case name"`
}

type createFromTemplateOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
}

// RegisterCreateToolFromTemplate loads JSON templates and registers dynamic tools (paper C3 subset, no in-process compile).
func RegisterCreateToolFromTemplate(r *Registry, store *DynamicStore, dataDir string, capRegister func(kind, name, scope string), onCompiled func(DynamicToolDef) error) error {
	tplDir := filepath.Join(dataDir, "tool_templates")
	_ = os.MkdirAll(tplDir, 0o755)

	t, err := utils.InferTool("create_tool_from_template",
		"Create a dynamic tool from a JSON template in data/tool_templates (parameters + description + code stub).",
		func(_ context.Context, in createFromTemplateInput) (createFromTemplateOutput, error) {
			tpl := strings.TrimSpace(in.Template)
			if tpl == "" {
				return createFromTemplateOutput{Success: false, Message: "template name required"}, nil
			}
			path := filepath.Join(tplDir, tpl+".json")
			if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(tplDir)) {
				return createFromTemplateOutput{Success: false, Message: "invalid template path"}, nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return createFromTemplateOutput{Success: false, Message: "template not found: " + err.Error()}, nil
			}
			var spec struct {
				ToolName    string `json:"tool_name"`
				Description string `json:"description"`
				Parameters  string `json:"parameters"`
				Code        string `json:"code"`
				UsageGuide  string `json:"usage_guide"`
			}
			if err := json.Unmarshal(raw, &spec); err != nil {
				return createFromTemplateOutput{Success: false, Message: err.Error()}, nil
			}
			name := strings.TrimSpace(in.ToolName)
			if name == "" {
				name = strings.TrimSpace(spec.ToolName)
			}
			name = strings.ToLower(name)
			if !toolNamePattern.MatchString(name) {
				return createFromTemplateOutput{Success: false, Message: "invalid tool_name"}, nil
			}
			def := DynamicToolDef{
				Name: name, Description: spec.Description, Parameters: spec.Parameters,
				Code: spec.Code, UsageGuide: spec.UsageGuide,
			}
			if store == nil {
				return createFromTemplateOutput{Success: false, Message: "dynamic store unavailable"}, nil
			}
			if err := store.Save(def); err != nil {
				return createFromTemplateOutput{Success: false, Message: err.Error()}, nil
			}
			if capRegister != nil {
				capRegister("tool", name, "agent")
			}
			if onCompiled != nil {
				if err := onCompiled(def); err != nil {
					return createFromTemplateOutput{Success: false, Message: err.Error()}, nil
				}
			}
			return createFromTemplateOutput{
				Success: true, Name: name,
				Message: fmt.Sprintf("tool %q created from template %q", name, tpl),
			}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
