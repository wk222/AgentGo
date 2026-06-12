package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
)

// MatrixState flows through the App Matrix Eino Compose graph (Ch.08 choreography).
type MatrixState struct {
	Event      capability.Event
	PlanAction string // coordinator | invoke_workflow | invoke_inner_app
	PlanTarget string
	PlanInput  string
	ExecOutput string
	Summary    string
	Error      string
}

// RunMatrixCompose orchestrates capability events via compose.Graph (plan → branch → execute → summarize).
func (rt *Runtime) RunMatrixCompose(ctx context.Context, ev capability.Event) (*MatrixState, error) {
	if rt == nil {
		return nil, fmt.Errorf("runtime nil")
	}
	runnable, err := compileMatrixGraph(ctx, rt)
	if err != nil {
		return nil, err
	}
	initial := &MatrixState{Event: ev}
	final, err := runnable.Invoke(ctx, initial)
	if err != nil {
		return final, err
	}
	return final, nil
}

func compileMatrixGraph(ctx context.Context, rt *Runtime) (compose.Runnable[*MatrixState, *MatrixState], error) {
	g := compose.NewGraph[*MatrixState, *MatrixState]()

	planFn := compose.InvokableLambda(func(ctx context.Context, st *MatrixState) (*MatrixState, error) {
		return matrixPlanNode(ctx, rt, st)
	})
	execFn := compose.InvokableLambda(func(ctx context.Context, st *MatrixState) (*MatrixState, error) {
		return matrixExecuteNode(ctx, rt, st)
	})
	sumFn := compose.InvokableLambda(func(ctx context.Context, st *MatrixState) (*MatrixState, error) {
		return matrixSummarizeNode(ctx, rt, st)
	})

	_ = g.AddLambdaNode("plan", planFn)
	_ = g.AddLambdaNode("execute", execFn)
	_ = g.AddLambdaNode("summarize", sumFn)

	branch := compose.NewGraphBranch(func(_ context.Context, st *MatrixState) (string, error) {
		switch strings.TrimSpace(st.PlanAction) {
		case "invoke_workflow", "invoke_inner_app":
			return "execute", nil
		default:
			return "summarize", nil
		}
	}, map[string]bool{"execute": false, "summarize": false})

	_ = g.AddEdge(compose.START, "plan")
	_ = g.AddBranch("plan", branch)
	_ = g.AddEdge("execute", "summarize")
	_ = g.AddEdge("summarize", compose.END)

	return g.Compile(ctx)
}

func matrixPlanNode(ctx context.Context, rt *Runtime, st *MatrixState) (*MatrixState, error) {
	out := *st
	plan, err := matrixLLMPlan(ctx, rt, st.Event)
	if err != nil {
		log.Printf("[app_matrix] plan fallback rules: %v", err)
		plan = matrixRulePlan(st.Event)
	}
	out.PlanAction = plan.Action
	out.PlanTarget = plan.Target
	out.PlanInput = plan.Input
	return &out, nil
}

type matrixPlan struct {
	Action string `json:"action"`
	Target string `json:"target"`
	Input  string `json:"input"`
}

func matrixRulePlan(ev capability.Event) matrixPlan {
	switch ev.Type {
	case "workflow.done":
		out := ev.Payload["output"]
		if len(out) > 4000 {
			out = out[:4000] + "…"
		}
		// If event names a follow-up workflow in metadata, run it
		if wf := strings.TrimSpace(ev.Payload["next_workflow_id"]); wf != "" {
			return matrixPlan{Action: "invoke_workflow", Target: wf, Input: out}
		}
		if app := strings.TrimSpace(ev.Payload["next_app"]); app != "" {
			return matrixPlan{Action: "invoke_inner_app", Target: app, Input: out}
		}
		return matrixPlan{
			Action: "coordinator",
			Input:  fmt.Sprintf("工作流 %s 已完成，输出：\n%s", ev.Source, out),
		}
	case "tool.compiled":
		return matrixPlan{
			Action: "coordinator",
			Input:  fmt.Sprintf("新工具 %s 已注册，请说明如何被其他 Agent 使用。", ev.Source),
		}
	case "app.registered":
		return matrixPlan{
			Action: "invoke_inner_app",
			Target: strings.TrimSpace(ev.Payload["name"]),
			Input:  "info",
		}
	default:
		return matrixPlan{Action: "coordinator", Input: "能力总线事件: " + ev.Type}
	}
}

func matrixLLMPlan(ctx context.Context, rt *Runtime, ev capability.Event) (matrixPlan, error) {
	cfg := rt.LLMConfig()
	if strings.TrimSpace(cfg.APIKey) == "" {
		return matrixPlan{}, fmt.Errorf("no api key")
	}
	payload, _ := json.Marshal(ev)
	system := `你是 App Matrix 编排器。根据能力总线事件，输出唯一一行 JSON：
{"action":"coordinator|invoke_workflow|invoke_inner_app","target":"<workflow_id|app_name|空>","input":"<传给目标或协调者的文本>"}
规则：
- workflow.done 且无明确后续：action=coordinator，input 含输出摘要
- 若应继续跑某工作流：action=invoke_workflow，target=workflow_id
- 若应调用某 inner app：action=invoke_inner_app，target=app_name`
	user := "事件:\n" + string(payload)
	raw, err := ChatOnce(ctx, cfg, system, user)
	if err != nil {
		return matrixPlan{}, err
	}
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	var plan matrixPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return matrixPlan{}, err
	}
	if plan.Action == "" {
		plan.Action = "coordinator"
	}
	return plan, nil
}

func matrixExecuteNode(ctx context.Context, rt *Runtime, st *MatrixState) (*MatrixState, error) {
	out := *st
	switch st.PlanAction {
	case "invoke_workflow":
		if rt.wfExec == nil {
			out.Error = "workflow executor unavailable"
			return &out, nil
		}
		res, err := rt.wfExec(ctx, st.PlanTarget, st.PlanInput)
		if err != nil {
			out.Error = err.Error()
		} else {
			out.ExecOutput = res
		}
	case "invoke_inner_app":
		res := rt.InvokeInnerApp(ctx, st.PlanTarget, st.PlanInput, "", "", "")
		if res.Error != "" {
			out.Error = res.Error
		} else {
			out.ExecOutput = res.Output
		}
	default:
		out.Error = "unknown execute action: " + st.PlanAction
	}
	return &out, nil
}

func matrixSummarizeNode(ctx context.Context, rt *Runtime, st *MatrixState) (*MatrixState, error) {
	out := *st
	if strings.TrimSpace(st.ExecOutput) != "" {
		out.Summary = st.ExecOutput
		return &out, nil
	}
	prompt := strings.TrimSpace(st.PlanInput)
	if prompt == "" {
		prompt = fmt.Sprintf("事件 %s 来源 %s", st.Event.Type, st.Event.Source)
	}
	runner := rt.agentRunner
	if runner == nil {
		out.Summary = prompt
		return &out, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	llm := rt.AgentLLMSettings()
	matrixMode := agent.SessionMode{Profile: agent.ModeAppMatrix, Canvas: agent.CanvasDeep}
	if agent.MatrixSupervisorEnabled() && strings.TrimSpace(llm.APIKey) != "" {
		sctx := agent.WithSessionMode(
			matrixTraceContext(cctx, agent.MatrixCoordinatorSession),
			matrixMode,
		)
		res, err := runner.RunMatrixSupervisor(sctx, llm, agent.MatrixCoordinatorSession, prompt)
		if err != nil {
			out.Error = err.Error()
			return &out, nil
		}
		if res != nil && res.PendingApproval != nil {
			rt.matrixRegisterPause(res, st.Event, prompt)
			out.Summary = strings.TrimSpace(res.Content)
			return &out, nil
		}
		if res != nil {
			out.Summary = res.Content
		}
		return &out, nil
	}
	gctx := agent.WithSessionMode(
		matrixTraceContext(cctx, agent.MatrixCoordinatorSession),
		matrixMode,
	)
	res, err := runner.Generate(gctx, llm, agent.MatrixCoordinatorSession, prompt, nil)
	if err != nil {
		out.Error = err.Error()
		return &out, nil
	}
	if res != nil {
		out.Summary = res.Content
	}
	return &out, nil
}
