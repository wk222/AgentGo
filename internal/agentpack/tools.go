package agentpack

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/tools"
)

type exportInput struct {
	Title     string   `json:"title" jsonschema:"description=A human-friendly title for this pack"`
	Workflows []string `json:"workflows" jsonschema:"description=Workflow ids or names to include"`
	Tools     []string `json:"tools" jsonschema:"description=Dynamic tool names to include. These carry executable code and the recipient must confirm on import."`
	Innerapps []string `json:"innerapps" jsonschema:"description=Inner app names to include. Their UI bundle and referenced workflow are bundled automatically."`
	OutPath   string   `json:"out_path" jsonschema:"description=Optional output file path. Defaults to the shared exports folder."`
}

type exportOutput struct {
	Path    string         `json:"path,omitempty"`
	Items   []ManifestItem `json:"items,omitempty"`
	Count   int            `json:"count"`
	Message string         `json:"message,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type inspectInput struct {
	Path string `json:"path" jsonschema:"description=Path to the .agentpack file to inspect"`
}

type inspectOutput struct {
	Title         string         `json:"title,omitempty"`
	Items         []ManifestItem `json:"items,omitempty"`
	HasExecutable bool           `json:"has_executable"`
	Error         string         `json:"error,omitempty"`
}

type importInput struct {
	Path      string `json:"path" jsonschema:"description=Path to the .agentpack file to import"`
	Confirm   bool   `json:"confirm" jsonschema:"description=Set true to install packs that contain executable tool code. Leave false to preview first."`
	Overwrite bool   `json:"overwrite" jsonschema:"description=Set true to replace existing workflows/tools/inner apps that have the same id or name. Defaults to skipping collisions."`
}

// RegisterTools wires the agent-facing export/inspect/import tools onto the registry.
func RegisterTools(reg *tools.Registry, e *Engine) error {
	if reg == nil || e == nil {
		return fmt.Errorf("agentpack: registry and engine required")
	}

	exp, err := utils.InferTool("export_agentpack",
		"Package agent-created workflows, dynamic tools, and/or inner apps into a portable .agentpack file that can be shared with other AgentGo users. Returns the saved file path. An inner app's referenced workflow is bundled automatically.",
		func(ctx context.Context, in exportInput) (exportOutput, error) {
			path, man, err := e.Export(ctx, ExportRequest{
				Title:               strings.TrimSpace(in.Title),
				Workflows:           in.Workflows,
				Tools:               in.Tools,
				InnerApps:           in.Innerapps,
				IncludeDependencies: true,
				OutPath:             strings.TrimSpace(in.OutPath),
			})
			if err != nil {
				return exportOutput{Error: err.Error()}, nil
			}
			return exportOutput{
				Path:    path,
				Items:   man.Items,
				Count:   len(man.Items),
				Message: fmt.Sprintf("exported %d item(s) to %s", len(man.Items), path),
			}, nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(exp)

	insp, err := utils.InferTool("inspect_agentpack",
		"Preview the contents of a .agentpack file (its items and whether it contains executable tool code) without installing anything.",
		func(ctx context.Context, in inspectInput) (inspectOutput, error) {
			man, err := Inspect(strings.TrimSpace(in.Path))
			if err != nil {
				return inspectOutput{Error: err.Error()}, nil
			}
			out := inspectOutput{Title: man.Title, Items: man.Items}
			out.HasExecutable = hasExecutableItems(man.Items)
			return out, nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(insp)

	imp, err := utils.InferTool("import_agentpack",
		"Install workflows, dynamic tools, and inner apps from a shared .agentpack file. If the pack contains executable tool code, it is NOT installed until you call again with confirm=true.",
		func(ctx context.Context, in importInput) (*ImportResult, error) {
			res, err := e.Import(ctx, strings.TrimSpace(in.Path), in.Confirm, in.Overwrite)
			if err != nil {
				return &ImportResult{Message: "import failed: " + err.Error()}, nil
			}
			return res, nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(imp)

	return nil
}
