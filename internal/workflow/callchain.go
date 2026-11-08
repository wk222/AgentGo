package workflow

import (
	"context"
	"fmt"
	"strings"

	"agentgo/internal/config"
)

type callChainKey struct{}

// MaxWorkflowDepth is how many workflows may be nested on one call path
// (workflow -> subworkflow -> custom node -> workflow ...). Agent-authored
// nodes can wrap whole systems, so an unbounded chain would burn model calls
// and side effects before anything noticed. AGENTGO_WORKFLOW_MAX_DEPTH
// overrides it; values outside 1..32 fall back to the default.
func MaxWorkflowDepth() int {
	n := config.WorkflowMaxDepth.Get()
	if n < 1 || n > 32 {
		return 6
	}
	return n
}

// CallChain returns the ids of the workflows running on this call path,
// outermost first. Context values survive the tool, subworkflow and custom
// node hops, so a workflow reached through a tool call still sees its callers.
func CallChain(ctx context.Context) []string {
	chain, _ := ctx.Value(callChainKey{}).([]string)
	return append([]string(nil), chain...)
}

// EnterWorkflow marks id as running and returns the derived context. It
// refuses a workflow that is already on the path (a cycle: the run could never
// finish by itself) and a path deeper than MaxWorkflowDepth.
func EnterWorkflow(ctx context.Context, id string) (context.Context, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx, nil
	}
	chain, _ := ctx.Value(callChainKey{}).([]string)
	for _, p := range chain {
		if p == id {
			return ctx, fmt.Errorf("workflow cycle: %s -> %s", strings.Join(chain, " -> "), id)
		}
	}
	if limit := MaxWorkflowDepth(); len(chain) >= limit {
		return ctx, fmt.Errorf("workflows nested too deep (limit %d): %s -> %s", limit, strings.Join(chain, " -> "), id)
	}
	next := make([]string, 0, len(chain)+1)
	next = append(next, chain...)
	next = append(next, id)
	return context.WithValue(ctx, callChainKey{}, next), nil
}

// workflowKey is the id a definition is known by on the call chain.
func workflowKey(def Definition) string {
	if id := strings.TrimSpace(def.ID); id != "" {
		return id
	}
	return strings.TrimSpace(def.Name)
}
