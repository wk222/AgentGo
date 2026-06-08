package agent

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/apps"
	"agentgo/internal/tools"
)

// RegisterAppBuilderTools registers verify_inner_app (assets) and build_inner_app_iteratively (orchestration).
func RegisterAppBuilderTools(
	r *tools.Registry,
	runner *Runner,
	b *apps.IterativeBuilder,
	llm func() LLMSettings,
	sessionID func(context.Context) string,
	onUpsert func(apps.InnerApp),
) error {
	if r == nil || b == nil || b.Scaffolder == nil {
		return nil
	}

	verifyTool, err := utils.InferTool("verify_inner_app",
		"Validate an inner app bundle (files + optional ping). Returns score and issues.",
		func(_ context.Context, in struct {
			AppName  string `json:"app_name"`
			SkipPing bool   `json:"skip_ping,omitempty"`
		}) (string, error) {
			var pinger apps.AppPinger
			if !in.SkipPing {
				pinger = b.Pinger
			}
			res := apps.VerifyBundle(context.Background(), b.Scaffolder.AppsRoot, in.AppName, pinger)
			raw, _ := json.Marshal(res)
			return string(raw), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(verifyTool)

	buildTool, err := utils.InferTool("build_inner_app_iteratively",
		`One-shot Inner App build: scaffold + auto-fix + app_builder sub-agent (file tools only) + verify.
Prefer over bare scaffold_inner_app when you need customized UI.`,
		func(ctx context.Context, in struct {
			Name          string `json:"name"`
			DisplayName   string `json:"display_name,omitempty"`
			Description   string `json:"description,omitempty"`
			Mode          string `json:"mode" jsonschema:"description=chat, static, or workflow"`
			WorkflowID    string `json:"workflow_id,omitempty"`
			SystemPrompt  string `json:"system_prompt,omitempty"`
			MaxIterations int    `json:"max_iterations,omitempty"`
			Overwrite     bool   `json:"overwrite,omitempty"`
		}) (string, error) {
			if runner == nil {
				raw, _ := json.Marshal(map[string]any{"success": false, "error": "runner unavailable"})
				return string(raw), nil
			}
			res := runner.BuildInnerAppFull(ctx, llm(), sessionID(ctx), b, apps.IterativeBuildOptions{
				Name: in.Name, DisplayName: in.DisplayName, Description: in.Description,
				Mode: in.Mode, WorkflowID: in.WorkflowID, SystemPrompt: in.SystemPrompt,
				MaxIterations: in.MaxIterations, Overwrite: in.Overwrite,
			})
			if res.Success && onUpsert != nil && b.Scaffolder.Store != nil {
				if app, err2 := b.Scaffolder.Store.GetByName(context.Background(), res.AppName); err2 == nil {
					onUpsert(app)
				}
			}
			raw, _ := json.Marshal(res)
			return string(raw), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(buildTool)
	return nil
}
