package tools

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/workflow"
)

// SyncAllWorkflowTools registers InferTool workflows and optional compose graph tools.
func SyncAllWorkflowTools(r *Registry, store *workflow.Store, runner WorkflowRunner, mkRC WorkflowGraphContextFactory) error {
	if err := SyncWorkflowTools(r, store, runner); err != nil {
		return err
	}
	return SyncWorkflowGraphTools(r, store, mkRC)
}

// WorkflowRunner executes stored workflows.
type WorkflowRunner = workflow.WorkflowRunner

type runWorkflowInput struct {
	WorkflowID string `json:"workflow_id"`
	Input      string `json:"input"`
}

type runWorkflowOutput struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func registerRunWorkflow(r *Registry, runner WorkflowRunner) error {
	if runner == nil {
		return nil
	}
	t, err := utils.InferTool("run_workflow",
		"Execute a saved workflow by id (PyFlow / Coze-style graph).",
		func(ctx context.Context, in runWorkflowInput) (runWorkflowOutput, error) {
			id := strings.TrimSpace(in.WorkflowID)
			if id == "" {
				return runWorkflowOutput{Error: "workflow_id required"}, nil
			}
			out, err := runner.Run(ctx, id, in.Input)
			if err != nil {
				return runWorkflowOutput{Error: err.Error()}, nil
			}
			return runWorkflowOutput{Output: out}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

// RegisterWorkflowTools adds run_workflow.
func RegisterWorkflowTools(r *Registry, store *workflow.Store, exec WorkflowRunner) error {
	if err := registerRunWorkflow(r, exec); err != nil {
		return err
	}
	return SyncWorkflowTools(r, store, exec)
}

type workflowAsToolInput struct {
	Input string `json:"input" jsonschema:"description=The raw text input to feed into this workflow"`
}

// WorkflowGraphContextFactory builds a per-workflow mutable RunContext for compose graph tools.
type WorkflowGraphContextFactory func(workflowID string) *workflow.RunContext

// SyncWorkflowGraphTools registers Eino compose graph tools (graphtool-equivalent).
func SyncWorkflowGraphTools(r *Registry, store *workflow.Store, mkRC WorkflowGraphContextFactory) error {
	if !workflow.GraphToolEnabled() || store == nil || mkRC == nil {
		return nil
	}
	r.RemoveToolsWithPrefix("graph_workflow_")

	defs, err := store.List()
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, def := range defs {
		rc := mkRC(def.ID)
		if rc == nil {
			continue
		}
		cleanName := strings.ReplaceAll(strings.ToLower(def.Name), " ", "_")
		toolName := "graph_workflow_" + strings.ReplaceAll(cleanName, "-", "_")
		desc := def.Description
		if desc == "" {
			desc = "Eino compose graph for workflow " + def.Name
		}
		gt, err := workflow.NewWorkflowGraphTool(ctx, def, rc, toolName, desc)
		if err == nil {
			r.AddTool(gt)
		}
	}
	return nil
}

// SyncWorkflowTools registers all workflows in the store as individual first-class tools.
func SyncWorkflowTools(r *Registry, store *workflow.Store, runner WorkflowRunner) error {
	if store == nil || runner == nil {
		return nil
	}
	r.RemoveToolsWithPrefix("workflow_")

	defs, err := store.List()
	if err != nil {
		return err
	}

	for _, def := range defs {
		cleanName := strings.ReplaceAll(strings.ToLower(def.Name), " ", "_")
		cleanName = strings.ReplaceAll(cleanName, "-", "_")
		toolName := "workflow_" + cleanName

		desc := def.Description
		if desc == "" {
			desc = "Execute saved workflow: " + def.Name
		}

		workflowID := def.ID
		t, err := utils.InferTool(toolName, desc, func(ctx context.Context, in workflowAsToolInput) (runWorkflowOutput, error) {
			out, err := runner.Run(ctx, workflowID, in.Input)
			if err != nil {
				return runWorkflowOutput{Error: err.Error()}, nil
			}
			return runWorkflowOutput{Output: out}, nil
		})
		if err == nil {
			r.AddTool(t)
		}
	}
	return nil
}
