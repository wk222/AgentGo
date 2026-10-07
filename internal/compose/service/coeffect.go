package service

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
)

// DynamicCoeffectTool wraps a BaseTool with spatial dependency declarations.
type DynamicCoeffectTool struct {
	tool.BaseTool
	requiredServices []string
}

// WrapWithCoeffects decorates a tool with required capability services.
func WrapWithCoeffects(t tool.BaseTool, requiredServices ...string) tool.BaseTool {
	return &DynamicCoeffectTool{
		BaseTool:         t,
		requiredServices: requiredServices,
	}
}

func (d *DynamicCoeffectTool) RequiredServices() []string {
	return d.requiredServices
}

// FilterAvailableTools filters out tools whose coeffect requirements are not currently met in the registry.
func FilterAvailableTools(ctx context.Context, tools []tool.BaseTool, reg Registry) []tool.BaseTool {
	if reg == nil || len(tools) == 0 {
		return tools
	}

	var available []tool.BaseTool
	for _, t := range tools {
		if ca, ok := t.(CoeffectAware); ok {
			reqs := ca.RequiredServices()
			if ok, _ := reg.CheckCoeffects(reqs...); !ok {
				continue
			}
		}
		available = append(available, t)
	}

	return available
}
