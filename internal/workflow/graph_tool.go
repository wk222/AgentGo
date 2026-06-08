package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// WorkflowRunner executes a stored workflow by id (implemented by bridge.Runtime).
type WorkflowRunner interface {
	Run(ctx context.Context, workflowID, input string) (string, error)
}

// GraphToolEnabled reports whether workflows register as Eino compose graph tools (default on).
func GraphToolEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_WORKFLOW_GRAPH_TOOL"))
	if v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	if v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	return true
}

// graphToolInput is the JSON args schema for workflow graph tools.
type graphToolInput struct {
	Input string `json:"input" jsonschema:"description=Workflow input text"`
}

// workflowGraphTool wraps a saved PyFlow workflow as tool.InvokableTool (Eino graphtool pattern).
//
// Each InvokableRun rebuilds the compose graph against a *private* RunContext copy
// (own Input, RunID, Vars map and signal closures). The compiled graph closes over
// its RunContext, so reusing one graph across calls — or mutating a shared rc per
// call, as an earlier version did — races and cross-contaminates concurrent runs.
// Per-invocation compilation is the same isolation pattern used by Execute/ResumeExecute.
type workflowGraphTool struct {
	info   *schema.ToolInfo
	def    Definition
	rcTmpl RunContext
}

// NewWorkflowGraphTool validates def and exposes it as an ADK tool. The supplied rc
// is captured as a read-only template of static dependencies (LLM/tool/agent runners,
// checkpoint store, event hook); it is never mutated and per-run state is derived from
// a copy on every InvokableRun.
func NewWorkflowGraphTool(ctx context.Context, def Definition, rc *RunContext, toolName, desc string) (tool.InvokableTool, error) {
	if rc == nil {
		return nil, fmt.Errorf("workflow graph tool: nil RunContext")
	}
	// Compile once up-front so malformed workflows fail fast at registration time.
	// The runnable is intentionally discarded — runs rebuild their own private graph.
	if _, err := CompileToCompose(ctx, def, rc); err != nil {
		return nil, err
	}
	if strings.TrimSpace(toolName) == "" {
		toolName = "graph_workflow_" + sanitizeToolName(def.Name)
	}
	if desc == "" {
		desc = "Execute saved workflow via Eino compose graph: " + def.Name
	}
	return &workflowGraphTool{
		info: &schema.ToolInfo{
			Name: toolName,
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"input": {Type: schema.String, Desc: "workflow input"},
			}),
		},
		def:    def,
		rcTmpl: *rc,
	}, nil
}

func (t *workflowGraphTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *workflowGraphTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var in graphToolInput
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &in)
	}
	input := strings.TrimSpace(in.Input)

	// Private per-invocation RunContext: copy the static template, then give this run
	// its own Input, RunID, Vars map and signal closures so concurrent invocations of
	// the same tool never share mutable state.
	rc := t.rcTmpl
	rc.Input = input
	rc.RunID = t.info.Name + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	rc.Vars = make(map[string]string, len(t.rcTmpl.Vars))
	for k, v := range t.rcTmpl.Vars {
		rc.Vars[k] = v
	}
	bus := DefaultSignalBus()
	runID := rc.RunID
	rc.SignalWait = func(ctx context.Context, name string, timeout time.Duration) (string, error) {
		return bus.Wait(ctx, runID, name, timeout)
	}
	rc.SignalEmit = func(ctx context.Context, name, payload string) {
		bus.Emit(runID, name, payload)
	}

	runnable, err := CompileToCompose(ctx, t.def, &rc)
	if err != nil {
		return "", err
	}

	state := &WorkflowState{OriginalInput: input, Vars: rc.Vars}
	wid := t.info.Name
	out, err := runnable.Invoke(ctx, state, invokeCheckpointOptions(&rc, wid)...)
	if err != nil {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return "", tool.CompositeInterrupt(ctx, "workflow:"+t.info.Name, nil, err)
		}
		return "", err
	}
	if out == nil {
		return "", nil
	}
	return out.LastOutput, nil
}

// NewRunnerShellGraphTool wraps WorkflowRunner.Run in a minimal START→run→END compose graph.
// Use when full PyFlow compile is unnecessary but Eino callbacks / CompositeInterrupt are desired.
func NewRunnerShellGraphTool(workflowID, toolName, desc string, runner WorkflowRunner) (tool.InvokableTool, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner required")
	}
	wid := strings.TrimSpace(workflowID)
	if wid == "" {
		return nil, fmt.Errorf("workflow_id required")
	}
	if strings.TrimSpace(toolName) == "" {
		toolName = "graph_shell_" + wid
	}
	g := compose.NewGraph[string, string]()
	runLambda := compose.InvokableLambda(func(ctx context.Context, in string) (string, error) {
		out, err := runner.Run(ctx, wid, in)
		if err != nil {
			if _, ok := compose.ExtractInterruptInfo(err); ok {
				return "", tool.CompositeInterrupt(ctx, "workflow_shell:"+wid, nil, err)
			}
			return "", err
		}
		return out, nil
	})
	_ = g.AddLambdaNode("run", runLambda)
	_ = g.AddEdge(compose.START, "run")
	_ = g.AddEdge("run", compose.END)
	runnable, err := g.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	return &runnerShellGraphTool{
		info: &schema.ToolInfo{
			Name: toolName,
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"input": {Type: schema.String, Desc: "workflow input"},
			}),
		},
		runnable: runnable,
	}, nil
}

type runnerShellGraphTool struct {
	info     *schema.ToolInfo
	runnable compose.Runnable[string, string]
}

func (t *runnerShellGraphTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *runnerShellGraphTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var in graphToolInput
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &in)
	}
	return t.runnable.Invoke(ctx, strings.TrimSpace(in.Input))
}

func sanitizeToolName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	if s == "" {
		return "unnamed"
	}
	return s
}
