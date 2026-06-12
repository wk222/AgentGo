package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
)

// InvokeJSON runs a registered tool by name with JSON arguments (workflow / matrix nodes).
func (r *Registry) InvokeJSON(ctx context.Context, name, argsJSON string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("tool registry unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("tool name required")
	}
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("tool %q not found", name)
	}
	inv, ok := t.(tool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("tool %q is not invokable", name)
	}
	return inv.InvokableRun(ctx, argsJSON)
}
