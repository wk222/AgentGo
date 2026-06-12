package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// WorkflowState represents the state passed between compose graph nodes.
type WorkflowState struct {
	OriginalInput string
	LastOutput    string
	Vars          map[string]string
}

// Event represents a workflow or step execution event.
type Event struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	NodeID    string `json:"node_id"`
	NodeType  string `json:"node_type"`
	Type      string `json:"type"` // "workflow_start", "workflow_done", "workflow_error", "node_start", "node_done", "node_error"
	Input     string `json:"input"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	Timestamp int64  `json:"timestamp"`
}

// RunContext carries dependencies for node execution.
type RunContext struct {
	LLMGenerate    func(ctx context.Context, prompt string) (string, error)
	AgentRun       func(ctx context.Context, sessionID, prompt string) (string, error)
	ToolRunner     ToolRunner
	RunSubworkflow func(ctx context.Context, workflowID, input string) (string, error)
	SignalWait     func(ctx context.Context, signalName string, timeout time.Duration) (string, error)
	SignalEmit     func(ctx context.Context, signalName, payload string)
	ScheduleCron   func(ctx context.Context, spec, prompt, sessionID string) (string, error)
	LeaseStore     *LeaseStore
	RunID          string
	Input          string
	Vars           map[string]string
	OnEvent        func(e Event)
	// CheckPointStore enables Eino compose checkpoint persist/resume (shared with ADK SQLite store).
	CheckPointStore compose.CheckPointStore
	CheckPointID    string
}

// NodeExecutor defines the interface for executing a specific type of workflow node.
type NodeExecutor interface {
	Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error)
}

// ExecutorRegistry holds registered node executors.
type ExecutorRegistry struct {
	executors map[string]NodeExecutor
}

// NewExecutorRegistry creates a registry with built-in node executors.
func NewExecutorRegistry() *ExecutorRegistry {
	r := &ExecutorRegistry{
		executors: make(map[string]NodeExecutor),
	}
	r.Register("start", &StartNodeExecutor{})
	r.Register("input", &StartNodeExecutor{})
	r.Register("llm", &LLMNodeExecutor{})
	r.Register("tool", &ToolNodeExecutor{})
	r.Register("code", &CodeNodeExecutor{})
	r.Register("transform", &CodeNodeExecutor{})
	r.Register("http", &HTTPNodeExecutor{})
	r.Register("http_request", &HTTPNodeExecutor{})
	r.Register("condition", &ConditionNodeExecutor{})
	r.Register("if", &ConditionNodeExecutor{})
	r.Register("branch", &ConditionNodeExecutor{})
	r.Register("switch", &ConditionNodeExecutor{})
	r.Register("end", &EndNodeExecutor{})
	r.Register("output", &EndNodeExecutor{})
	r.Register("variable", &VariableNodeExecutor{})
	r.Register("set_var", &VariableNodeExecutor{})
	r.Register("json", &JSONNodeExecutor{})
	r.Register("json_parse", &JSONNodeExecutor{})
	r.Register("delay", &DelayNodeExecutor{})
	r.Register("sleep", &DelayNodeExecutor{})
	r.Register("subworkflow", &SubworkflowNodeExecutor{})
	r.Register("workflow", &SubworkflowNodeExecutor{})
	r.Register("bash", &BashNodeExecutor{})
	r.Register("database", &DatabaseNodeExecutor{})
	r.Register("sqlite", &DatabaseNodeExecutor{})
	r.Register("agent", &AgentNodeExecutor{})
	r.Register("adk_agent", &AgentNodeExecutor{})
	r.Register("wait_signal", &WaitSignalNodeExecutor{})
	r.Register("emit_signal", &EmitSignalNodeExecutor{})
	r.Register("ask_user", &AskUserNodeExecutor{})
	r.Register("human", &AskUserNodeExecutor{})
	r.Register("hitl", &AskUserNodeExecutor{})
	r.Register("signal", &WaitSignalNodeExecutor{})
	r.Register("debate", &DebateNodeExecutor{})
	r.Register("multi_agent", &DebateNodeExecutor{})
	r.Register("cron", &CronNodeExecutor{})
	r.Register("schedule", &CronNodeExecutor{})
	r.Register("acquire_lease", &AcquireLeaseNodeExecutor{})
	r.Register("release_lease", &ReleaseLeaseNodeExecutor{})
	r.Register("lease", &AcquireLeaseNodeExecutor{})
	r.Register("notify", &NotifyNodeExecutor{})
	r.Register("notification", &NotifyNodeExecutor{})
	r.Register("monitor", &MonitorNodeExecutor{})
	r.Register("watch", &MonitorNodeExecutor{})
	r.Register("data_source", &DataSourceNodeExecutor{})
	r.Register("datasource", &DataSourceNodeExecutor{})
	r.Register("data", &DataSourceNodeExecutor{})
	r.Register("loop", &LoopNodeExecutor{})
	r.Register("iteration", &LoopNodeExecutor{})
	r.Register("foreach", &LoopNodeExecutor{})
	r.Register("batch", &BatchNodeExecutor{})
	r.Register("parallel", &ParallelNodeExecutor{})
	r.Register("merge", &MergeNodeExecutor{})
	return r
}

func (r *ExecutorRegistry) Register(typ string, exec NodeExecutor) {
	r.executors[strings.ToLower(typ)] = exec
}

func (r *ExecutorRegistry) Get(typ string) NodeExecutor {
	return r.executors[strings.ToLower(typ)]
}

// Execute runs a workflow definition sequentially by compiling it into an Eino Compose Graph.
func Execute(ctx context.Context, def Definition, rc RunContext) (string, error) {
	if rc.OnEvent == nil {
		rc.OnEvent = func(e Event) {}
	}
	if rc.Vars == nil {
		rc.Vars = make(map[string]string)
	}

	rcPtr := &rc
	runnable, err := CompileToCompose(ctx, def, rcPtr)
	if err != nil {
		return "", err
	}

	initialState := &WorkflowState{
		OriginalInput: rc.Input,
		LastOutput:    "",
		Vars:          rc.Vars,
	}

	wid := strings.TrimSpace(def.ID)
	if wid == "" {
		wid = def.Name
	}
	finalState, err := runnable.Invoke(ctx, initialState, invokeCheckpointOptions(rcPtr, wid)...)
	if err != nil {
		return "", wrapInterrupt(wid, rcPtr, err)
	}

	if finalState != nil {
		return finalState.LastOutput, nil
	}
	return "", nil
}

// CompileToCompose translates a PyFlow-like Definition into an Eino Compose RunnableGraph.
// Optional compileOpts (e.g. compose.WithInterruptAfterNodes) are for tests or advanced HITL wiring.
func CompileToCompose(ctx context.Context, def Definition, rc *RunContext, compileOpts ...compose.GraphCompileOption) (compose.Runnable[*WorkflowState, *WorkflowState], error) {
	if rc == nil {
		return nil, fmt.Errorf("nil RunContext")
	}
	g := compose.NewGraph[*WorkflowState, *WorkflowState]()
	registry := NewExecutorRegistry()

	nodeByID := make(map[string]Node, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}

	// Edges grouped by 'From'
	next := map[string][]Edge{}
	for _, e := range def.Edges {
		next[e.From] = append(next[e.From], e)
	}

	// 1. Add all nodes
	for _, n := range def.Nodes {
		node := n // capture loop variable
		exec := registry.Get(node.Type)
		if exec == nil {
			return nil, fmt.Errorf("unknown node type: %s", node.Type)
		}

		lambda := compose.InvokableLambda(func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			if state == nil {
				state = &WorkflowState{OriginalInput: rc.Input, Vars: rc.Vars}
			}
			stepID := uuid.NewString()
			stepInput := state.LastOutput
			if stepInput == "" {
				stepInput = state.OriginalInput
			}
			if rc.OnEvent != nil {
				rc.OnEvent(Event{
					ID:        stepID,
					RunID:     rc.RunID,
					NodeID:    node.ID,
					NodeType:  node.Type,
					Type:      "node_start",
					Input:     stepInput,
					Timestamp: time.Now().Unix(),
				})
			}
			out, err := exec.Execute(ctx, node, state.OriginalInput, state.LastOutput, *rc)
			if err != nil {
				if rc.OnEvent != nil {
					rc.OnEvent(Event{
						ID:        stepID,
						RunID:     rc.RunID,
						NodeID:    node.ID,
						NodeType:  node.Type,
						Type:      "node_error",
						Input:     stepInput,
						Error:     err.Error(),
						Timestamp: time.Now().Unix(),
					})
				}
				return nil, err
			}
			if rc.OnEvent != nil {
				rc.OnEvent(Event{
					ID:        stepID,
					RunID:     rc.RunID,
					NodeID:    node.ID,
					NodeType:  node.Type,
					Type:      "node_done",
					Input:     stepInput,
					Output:    out,
					Timestamp: time.Now().Unix(),
				})
			}

			newState := &WorkflowState{
				OriginalInput: state.OriginalInput,
				LastOutput:    out,
				Vars:          rc.Vars, // shared
			}
			return newState, nil
		})

		_ = g.AddLambdaNode(node.ID, lambda)
	}

	// 2. Add edges and branches
	for _, n := range def.Nodes {
		edges := next[n.ID]
		if len(edges) == 0 {
			// No outgoing edges -> connects to END
			_ = g.AddEdge(n.ID, compose.END)
			continue
		}

		nt := strings.ToLower(n.Type)
		isCondition := nt == "condition" || nt == "if" || nt == "branch" || nt == "switch"
		isParallel := nt == "parallel"

		if isParallel && len(edges) > 1 {
			mergeTarget := ""
			if n.Config != nil {
				mergeTarget, _ = n.Config["merge_target"].(string)
			}
			if strings.TrimSpace(mergeTarget) != "" {
				_ = g.AddEdge(n.ID, mergeTarget)
			} else {
				_ = g.AddEdge(n.ID, compose.END)
			}
			continue
		}

		if !isCondition && len(edges) == 1 {
			// Simple edge
			_ = g.AddEdge(n.ID, edges[0].To)
		} else {
			// Branching
			nodeEdges := edges
			capturedNode := n
			condition := func(ctx context.Context, state *WorkflowState) (string, error) {
				nextID := pickNextEdge(capturedNode, state.LastOutput, nodeEdges)
				if nextID == "" {
					return compose.END, nil
				}
				return nextID, nil
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

	// 3. Connect START
	startNode := "start"
	if _, ok := nodeByID[startNode]; !ok && len(def.Nodes) > 0 {
		startNode = def.Nodes[0].ID
	}
	_ = g.AddEdge(compose.START, startNode)

	opts := []compose.GraphCompileOption{compose.WithGraphName(workflowGraphName(def))}
	if rc.CheckPointStore != nil {
		opts = append(opts, compose.WithCheckPointStore(rc.CheckPointStore))
	}
	opts = append(opts, compileOpts...)
	return g.Compile(ctx, opts...)
}

func pickNextEdge(node Node, lastOutput string, edges []Edge) string {
	if len(edges) == 0 {
		return ""
	}
	nt := strings.ToLower(node.Type)
	if nt == "condition" || nt == "if" || nt == "branch" || nt == "switch" {
		ordered := orderEdgesByBranchRules(node, edges)
		for _, e := range ordered {
			if edgeMatches(node, e, lastOutput) {
				return e.To
			}
		}
		for _, e := range ordered {
			if isDefaultBranchEdge(node, e) {
				return e.To
			}
		}
		if len(ordered) > 0 {
			return ordered[0].To
		}
		return ""
	}
	return edges[0].To
}

func edgeMatches(node Node, e Edge, lastOutput string) bool {
	when := resolveWhenForEdge(node, e)
	if when == "" && node.Config != nil {
		if m, ok := node.Config["cases"].(map[string]any); ok {
			for k, v := range m {
				if target, ok := v.(string); ok && target == e.To && matchWhen(k, lastOutput) {
					return true
				}
			}
		}
	}
	if when == "" {
		return false
	}
	return matchWhen(when, lastOutput)
}

func matchWhen(expr, lastOutput string) bool {
	expr = strings.TrimSpace(strings.ToLower(expr))
	lo := strings.ToLower(lastOutput)
	switch {
	case strings.HasPrefix(expr, "contains:"):
		return strings.Contains(lo, strings.TrimPrefix(expr, "contains:"))
	case strings.HasPrefix(expr, "equals:"):
		return lo == strings.TrimPrefix(expr, "equals:")
	case strings.HasPrefix(expr, "not_equals:"):
		return lo != strings.TrimPrefix(expr, "not_equals:")
	case strings.HasPrefix(expr, "starts_with:"):
		return strings.HasPrefix(lo, strings.TrimPrefix(expr, "starts_with:"))
	case strings.HasPrefix(expr, "ends_with:"):
		return strings.HasSuffix(lo, strings.TrimPrefix(expr, "ends_with:"))
	case expr == "true", expr == "yes", expr == "1":
		return lastOutput != ""
	case expr == "false", expr == "no", expr == "0":
		return lastOutput == ""
	default:
		return strings.Contains(lo, expr)
	}
}

func expandTemplate(s, input, last string) string {
	return expandTemplateVars(s, input, last, nil)
}

func expandTemplateVars(s, input, last string, vars map[string]string) string {
	s = strings.ReplaceAll(s, "{{input}}", input)
	s = strings.ReplaceAll(s, "{{last}}", last)
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{var."+k+"}}", v)
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// RunResultEvent serializes execution summary.
func RunResultEvent(output string, err error) string {
	m := map[string]any{"output": output}
	if err != nil {
		m["error"] = err.Error()
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// StateToMessages converts workflow state to schema messages for compose graphs.
func StateToMessages(input, output string) []*schema.Message {
	return []*schema.Message{
		schema.UserMessage(input),
		schema.AssistantMessage(output, nil),
	}
}
