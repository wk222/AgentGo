package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/apps"
)

// RegisterMatrixOrchestrationTools exposes PyBot-style app orchestration CRUD to the agent.
func RegisterMatrixOrchestrationTools(reg *Registry, store *apps.Store, runner apps.MatrixRunner) error {
	tools := []tool.BaseTool{
		newMatrixListTool(store),
		newMatrixGetTool(store),
		newMatrixSaveTool(store),
		newMatrixValidateTool(store),
		newMatrixRunTool(store, runner),
	}
	for _, t := range tools {
		reg.AddTool(t)
	}
	return nil
}

type matrixListTool struct{ store *apps.Store }

func newMatrixListTool(store *apps.Store) *matrixListTool { return &matrixListTool{store: store} }

func (t *matrixListTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "list_matrix_orchestrations",
		Desc: "List saved App Matrix orchestration graphs (name, id, node count).",
	}, nil
}

func (t *matrixListTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	list, err := t.store.ListMatrices(ctx)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(list)
	return string(b), nil
}

type matrixGetIn struct {
	Name string `json:"name" jsonschema:"description=orchestration name"`
}

type matrixGetTool struct{ store *apps.Store }

func newMatrixGetTool(store *apps.Store) *matrixGetTool { return &matrixGetTool{store: store} }

func (t *matrixGetTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_matrix_orchestration",
		Desc: "Load an App Matrix orchestration graph by name.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {Type: schema.String, Desc: "orchestration name"},
		}),
	}, nil
}

func (t *matrixGetTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in matrixGetIn
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", err
	}
	m, err := t.store.GetMatrixByName(ctx, strings.TrimSpace(in.Name))
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}

type matrixSaveIn struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Nodes       []apps.MatrixNode `json:"nodes"`
	Edges       []apps.MatrixEdge `json:"edges"`
}

type matrixSaveTool struct{ store *apps.Store }

func newMatrixSaveTool(store *apps.Store) *matrixSaveTool { return &matrixSaveTool{store: store} }

func (t *matrixSaveTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "save_matrix_orchestration",
		Desc: "Save or update an App Matrix orchestration graph (nodes + edges).",
	}, nil
}

func (t *matrixSaveTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in matrixSaveIn
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Name) == "" {
		return "", fmt.Errorf("name required")
	}
	m := apps.MatrixOrchestration{
		Name: in.Name, Description: in.Description, Nodes: in.Nodes, Edges: in.Edges,
	}
	if err := apps.ValidateMatrixOrchestration(m); err != nil {
		return "", err
	}
	if err := t.store.SaveMatrix(ctx, m); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"name":%q,"nodes":%d,"edges":%d}`, m.Name, len(m.Nodes), len(m.Edges)), nil
}

type matrixValidateIn struct {
	Name string `json:"name,omitempty"`
	JSON string `json:"json,omitempty" jsonschema:"description=inline graph JSON alternative to name"`
}

type matrixValidateTool struct{ store *apps.Store }

func newMatrixValidateTool(store *apps.Store) *matrixValidateTool {
	return &matrixValidateTool{store: store}
}

func (t *matrixValidateTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "validate_matrix_orchestration",
		Desc: "Validate orchestration topology (node ids, edges, app_id presence).",
	}, nil
}

func (t *matrixValidateTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in matrixValidateIn
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", err
	}
	var m apps.MatrixOrchestration
	if strings.TrimSpace(in.JSON) != "" {
		if err := json.Unmarshal([]byte(in.JSON), &m); err != nil {
			return "", err
		}
	} else if name := strings.TrimSpace(in.Name); name != "" {
		got, err := t.store.GetMatrixByName(ctx, name)
		if err != nil {
			return "", err
		}
		m = *got
	} else {
		return "", fmt.Errorf("provide name or json")
	}
	if err := apps.ValidateMatrixOrchestration(m); err != nil {
		return fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error()), nil
	}
	return `{"ok":true}`, nil
}

type matrixRunIn struct {
	Name  string `json:"name"`
	Input string `json:"input,omitempty"`
}

type matrixRunTool struct {
	store  *apps.Store
	runner apps.MatrixRunner
}

func newMatrixRunTool(store *apps.Store, runner apps.MatrixRunner) *matrixRunTool {
	return &matrixRunTool{store: store, runner: runner}
}

func (t *matrixRunTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "run_matrix_orchestration",
		Desc: "Execute a saved App Matrix orchestration graph with optional input.",
	}, nil
}

func (t *matrixRunTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	if t.runner == nil {
		return "", fmt.Errorf("matrix runner unavailable")
	}
	var in matrixRunIn
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", err
	}
	m, err := t.store.GetMatrixByName(ctx, strings.TrimSpace(in.Name))
	if err != nil {
		return "", err
	}
	out, err := apps.ExecuteMatrix(ctx, t.store, *m, t.runner, in.Input)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"output":%q}`, out), nil
}
