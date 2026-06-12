package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
)

// MatrixState is the state flowing through the App Matrix Compose graph.
type MatrixState struct {
	GlobalInput string
	LastOutput  string
	Variables   map[string]string
	Trace       []string
}

// MatrixRunner wraps the invocation of inner apps for the graph nodes.
type MatrixRunner interface {
	InvokeApp(ctx context.Context, appID, capability, input string) (InvokeResult, error)
}

// CompileMatrixToCompose translates a MatrixOrchestration into an Eino Compose RunnableGraph.
func CompileMatrixToCompose(ctx context.Context, store *Store, matrix MatrixOrchestration, runner MatrixRunner) (compose.Runnable[*MatrixState, *MatrixState], error) {
	g := compose.NewGraph[*MatrixState, *MatrixState]()

	// 1. Map nodes by ID and prepare edge lookup
	nodes := make(map[string]MatrixNode)
	for _, n := range matrix.Nodes {
		nodes[n.ID] = n
	}

	next := make(map[string][]MatrixEdge)
	for _, e := range matrix.Edges {
		next[e.From] = append(next[e.From], e)
	}

	// 2. Add Lambda Nodes
	for _, n := range matrix.Nodes {
		node := n // capture loop variable

		lambda := compose.InvokableLambda(func(ctx context.Context, state *MatrixState) (*MatrixState, error) {
			// Resolve input (e.g. from previous output or global)
			input := state.LastOutput
			if input == "" {
				input = state.GlobalInput
			}

			// Invoke the InnerApp
			res, err := runner.InvokeApp(ctx, node.AppID, node.Capability, input)
			if err != nil {
				return nil, fmt.Errorf("matrix node %s (app %s) failed: %w", node.ID, node.AppID, err)
			}
			if res.Error != "" {
				return nil, fmt.Errorf("matrix node %s (app %s) returned error: %s", node.ID, node.AppID, res.Error)
			}

			// Update state
			newState := &MatrixState{
				GlobalInput: state.GlobalInput,
				LastOutput:  res.Output,
				Variables:   state.Variables,
				Trace:       append(state.Trace, fmt.Sprintf("Node %s (App %s): Success", node.ID, node.AppID)),
			}
			return newState, nil
		})

		_ = g.AddLambdaNode(node.ID, lambda)
	}

	// 3. Connect Edges
	for _, n := range matrix.Nodes {
		edges := next[n.ID]
		if len(edges) == 0 {
			_ = g.AddEdge(n.ID, compose.END)
			continue
		}

		if len(edges) == 1 && edges[0].When == "" {
			_ = g.AddEdge(n.ID, edges[0].To)
		} else {
			// Branching
			nodeEdges := edges
			condition := func(ctx context.Context, state *MatrixState) (string, error) {
				// Simple condition evaluation based on LastOutput
				for _, e := range nodeEdges {
					if e.When == "" {
						return e.To, nil // default fallback
					}
					// Evaluate e.When (e.g. "contains:success")
					if matchMatrixCondition(e.When, state.LastOutput) {
						return e.To, nil
					}
				}
				return compose.END, nil
			}

			ends := make(map[string]bool)
			for _, e := range nodeEdges {
				ends[e.To] = false
			}
			ends[compose.END] = true

			branch := compose.NewGraphBranch(condition, ends)
			_ = g.AddBranch(n.ID, branch)
		}
	}

	// 4. Set START node (defaults to the first node in the list if no explicit start pointer exists)
	startNode := "start"
	if _, ok := nodes[startNode]; !ok && len(matrix.Nodes) > 0 {
		startNode = matrix.Nodes[0].ID
	}
	if len(matrix.Nodes) > 0 {
		_ = g.AddEdge(compose.START, startNode)
	}

	return g.Compile(ctx)
}

// ExecuteMatrix is a helper to compile and run a Matrix orchestration directly.
func ExecuteMatrix(ctx context.Context, store *Store, matrix MatrixOrchestration, runner MatrixRunner, input string) (string, error) {
	runnable, err := CompileMatrixToCompose(ctx, store, matrix, runner)
	if err != nil {
		return "", err
	}

	initialState := &MatrixState{
		GlobalInput: input,
		LastOutput:  "",
		Variables:   make(map[string]string),
		Trace:       make([]string, 0),
	}

	finalState, err := runnable.Invoke(ctx, initialState)
	if err != nil {
		return "", err
	}
	if finalState != nil {
		return finalState.LastOutput, nil
	}
	return "", nil
}

func matchMatrixCondition(expr, output string) bool {
	// Simple matching logic (similar to workflow conditions)
	// You can expand this with a proper expression engine if needed.
	expr = strings.TrimSpace(strings.ToLower(expr))
	out := strings.ToLower(output)

	if strings.HasPrefix(expr, "contains:") {
		return strings.Contains(out, strings.TrimPrefix(expr, "contains:"))
	}
	if expr == "true" {
		var m map[string]any
		if json.Unmarshal([]byte(output), &m) == nil {
			if v, ok := m["success"].(bool); ok && v {
				return true
			}
		}
	}
	return out == expr
}
