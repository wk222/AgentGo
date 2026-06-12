package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentgo/internal/agent"
	"agentgo/internal/gateway"
	"agentgo/internal/workflow"
)

// RuntimeGateway implements gateway.Backend.
type RuntimeGateway struct {
	rt     *Runtime
	broker *gateway.Broker
}

func NewRuntimeGateway(rt *Runtime, broker *gateway.Broker) *RuntimeGateway {
	return &RuntimeGateway{rt: rt, broker: broker}
}

func (g *RuntimeGateway) ChatModelName() string {
	cfg := g.rt.LLMConfig()
	if strings.TrimSpace(cfg.Model) != "" {
		return cfg.Model
	}
	return "agentgo"
}

func (g *RuntimeGateway) StreamChat(ctx context.Context, req gateway.ChatRequest, emit func(event string, data []byte) error) (*gateway.ChatResult, error) {
	runner := g.rt.AgentRunner()
	if runner == nil {
		return nil, fmt.Errorf("agent runner unavailable")
	}
	cfg := g.rt.LLMConfig()
	if cfg.APIKey == "" {
		answer, err := ChatOnce(ctx, cfg, "", req.Message)
		if err != nil {
			return nil, err
		}
		_ = emit("chunk", mustJSON(map[string]string{"delta": answer}))
		return &gateway.ChatResult{Content: answer}, nil
	}
	llm := agent.LLMSettings{APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model}
	var full string
	res, err := runner.GenerateStream(ctx, llm, req.SessionID, req.Message, nil, func(delta string) {
		full += delta
		_ = emit("chunk", mustJSON(map[string]string{"delta": delta, "session_id": req.SessionID}))
	})

	if err != nil {
		return nil, err
	}
	content := full
	if res != nil && res.Content != "" {
		content = res.Content
	}
	if res != nil && res.PendingApproval != nil {
		_ = emit("interrupt", mustJSON(map[string]any{
			"approval_id": res.PendingApproval.ApprovalID,
			"tool_name":   res.PendingApproval.ToolName,
		}))
	}
	return &gateway.ChatResult{Content: content}, nil
}

func (g *RuntimeGateway) ReplayTaskEvents(ctx context.Context, taskID string, afterSeq int64, emit func(event string, data []byte) error) error {
	if g.rt.taskHub == nil {
		return fmt.Errorf("task hub unavailable")
	}
	events, err := g.rt.taskHub.EventsSince(taskID, afterSeq)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := emit(ev.Type, mustJSON(ev)); err != nil {
			return err
		}
	}
	return nil
}

func (g *RuntimeGateway) RegisterTaskListener(taskID string, emit func(event string, data []byte)) func() {
	if g.broker == nil {
		return func() {}
	}
	return g.broker.Subscribe(taskID, emit)
}

func (g *RuntimeGateway) ListWorkflows(ctx context.Context) ([]byte, error) {
	if g.rt.wfStore == nil {
		return []byte("[]"), nil
	}
	list, err := g.rt.wfStore.List()
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

func (g *RuntimeGateway) GetWorkflowFlowgram(ctx context.Context, id string) ([]byte, error) {
	if g.rt.wfStore == nil {
		return nil, fmt.Errorf("workflow store unavailable")
	}
	doc, err := g.rt.wfStore.GetFlowgram(id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

func (g *RuntimeGateway) SaveWorkflowFlowgram(ctx context.Context, id string, flowgramJSON []byte) error {
	if g.rt.wfStore == nil {
		return fmt.Errorf("workflow store unavailable")
	}
	var doc workflow.FlowgramDocument
	if err := json.Unmarshal(flowgramJSON, &doc); err != nil {
		return err
	}
	return g.rt.wfStore.SaveFlowgram(id, doc)
}

func (g *RuntimeGateway) RunWorkflow(ctx context.Context, id, input string) (string, error) {
	if g.rt.wfExec == nil {
		return "", fmt.Errorf("workflow executor unavailable")
	}
	return g.rt.wfExec(ctx, id, input)
}

func (g *RuntimeGateway) CancelSessionRun(ctx context.Context, sessionID string) bool {
	runner := g.rt.AgentRunner()
	if runner != nil {
		return runner.CancelSessionRun(sessionID)
	}
	return false
}

func (g *RuntimeGateway) ListInnerApps(ctx context.Context) ([]byte, error) {
	if g.rt.appStore == nil {
		return []byte("[]"), nil
	}
	list, err := g.rt.appStore.List(ctx, 100)
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

func (g *RuntimeGateway) GetInnerApp(ctx context.Context, name string) ([]byte, error) {
	if g.rt.appStore == nil {
		return nil, fmt.Errorf("app store unavailable")
	}
	app, err := g.rt.appStore.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(app)
}

func (g *RuntimeGateway) InvokeInnerApp(ctx context.Context, name, input, capability, action, payloadJSON string) ([]byte, error) {
	res := g.rt.InvokeInnerApp(ctx, name, input, capability, action, payloadJSON)
	return json.Marshal(invokeResultMap(res))
}

func (g *RuntimeGateway) GetInnerAppAsset(ctx context.Context, name, relPath string) ([]byte, string, error) {
	return g.rt.ReadInnerAppBundleFile(ctx, name, relPath)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
