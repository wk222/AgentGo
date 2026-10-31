package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/engine"
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
	if g.rt.engineRouter == nil {
		return nil, fmt.Errorf("engine router unavailable")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.Harness != "" {
		h, ok := agent.ResolveHarness(req.Harness)
		if !ok {
			return nil, fmt.Errorf("unknown harness %q", req.Harness)
		}
		if req.SessionID != "" {
			if runner := g.rt.AgentRunner(); runner != nil {
				runner.SetSessionModeForSession(req.SessionID, h.Mode())
			}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	meter := &agent.RunMeter{}
	ctx = agent.WithRunMeter(ctx, meter)
	var streamMu sync.Mutex
	var streamErr error
	res := g.rt.runEngineTurn(ctx, engine.RunRequest{
		Engine: EinoEngineID, SessionID: req.SessionID, Input: req.Message,
	}, engine.SinkFunc(func(ev engine.Event) {
		event := ""
		switch ev.Type {
		case engine.EventToken:
			event = "chunk"
		case engine.EventApprovalRequest:
			event = "interrupt"
		}
		if event == "" || emit == nil {
			return
		}
		payload := make(map[string]any, len(ev.Payload)+1)
		for k, v := range ev.Payload {
			payload[k] = v
		}
		payload["session_id"] = req.SessionID
		streamMu.Lock()
		defer streamMu.Unlock()
		if streamErr == nil {
			streamErr = emit(event, mustJSON(payload))
			if streamErr != nil {
				cancel()
			}
		}
	}))
	streamMu.Lock()
	err := streamErr
	streamMu.Unlock()
	if err != nil {
		return nil, err
	}
	var content []string
	for _, m := range res.Messages {
		if m.Role == "assistant" && (m.Type == "" || m.Type == "text") {
			content = append(content, m.Content)
		}
	}
	meter.Wait(2 * time.Second)
	u := meter.Snapshot()
	return &gateway.ChatResult{Content: strings.Join(content, "\n\n"), Error: res.Error, Degraded: res.Degraded, Usage: &gateway.UsageStats{
		ModelCalls: u.ModelCalls, PromptTokens: u.PromptTokens, CachedTokens: u.CachedTokens,
		CompletionTokens: u.CompletionTokens, ReasoningTokens: u.ReasoningTokens, ToolCalls: u.ToolCalls,
		Spills: u.Spills, SpilledBytes: u.SpilledBytes, SavedBytes: u.SavedBytes,
	}}, nil
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
	return g.rt.cancelSessionRun(sessionID)
}

// Cancel the shared turn (including prompt hooks) and any legacy/resumed ADK
// segment which has not entered through the router.
func (rt *Runtime) cancelSessionRun(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	cancelled := false
	if rt.engineRouter != nil {
		cancelled = rt.engineRouter.CancelSession(sessionID) > 0
	}
	if runner := rt.AgentRunner(); runner != nil {
		cancelled = runner.CancelSessionRun(sessionID) || cancelled
	}
	return cancelled
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
