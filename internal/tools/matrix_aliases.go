package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/apps"
)

// RegisterMatrixAliasTools adds PyBot-style orchestration tool name aliases.
func RegisterMatrixAliasTools(reg *Registry, store *apps.Store, runner apps.MatrixRunner) error {
	aliases := []struct {
		name   string
		target string
		desc   string
	}{
		{"register_matrix_orchestration", "save_matrix_orchestration", "Alias: save orchestration graph"},
		{"topology_validate", "validate_matrix_orchestration", "Alias: validate topology"},
		{"register_pipeline", "save_matrix_orchestration", "Alias: register pipeline graph"},
		{"list_pipelines", "list_matrix_orchestrations", "Alias: list saved pipelines"},
		{"run_pipeline", "run_matrix_orchestration", "Alias: run pipeline"},
		{"register_matrix_node", "save_matrix_orchestration", "Alias: register a node into orchestration graph"},
	}
	for _, a := range aliases {
		if err := reg.registerAlias(a.name, a.target, a.desc, store, runner); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) registerAlias(name, target, desc string, store *apps.Store, runner apps.MatrixRunner) error {
	t, err := newMatrixAliasTool(name, target, desc, store, runner)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type matrixAliasTool struct {
	name   string
	target string
	desc   string
	inner  tool.BaseTool
}

func newMatrixAliasTool(name, target, desc string, store *apps.Store, runner apps.MatrixRunner) (tool.BaseTool, error) {
	var inner tool.BaseTool
	switch target {
	case "save_matrix_orchestration":
		inner = newMatrixSaveTool(store)
	case "validate_matrix_orchestration":
		inner = newMatrixValidateTool(store)
	case "list_matrix_orchestrations":
		inner = newMatrixListTool(store)
	case "run_matrix_orchestration":
		inner = newMatrixRunTool(store, runner)
	default:
		return nil, fmt.Errorf("unknown alias target %q", target)
	}
	return &matrixAliasTool{name: name, target: target, desc: desc, inner: inner}, nil
}

func (t *matrixAliasTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.inner.Info(ctx)
	if err != nil {
		return nil, err
	}
	cp := *info
	cp.Name = t.name
	if t.desc != "" {
		cp.Desc = t.desc + " (→ " + t.target + ")"
	}
	return &cp, nil
}

func (t *matrixAliasTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	type invoker interface {
		InvokableRun(context.Context, string, ...tool.Option) (string, error)
	}
	if iv, ok := t.inner.(invoker); ok {
		return iv.InvokableRun(ctx, args, opts...)
	}
	b, _ := json.Marshal(map[string]string{"error": "alias target not invokable"})
	return string(b), nil
}
