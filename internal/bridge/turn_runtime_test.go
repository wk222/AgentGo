package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/engine"
	"agentgo/internal/evidence"
	"agentgo/internal/gateway"
	"agentgo/internal/governance"
	"agentgo/internal/sessions"
	"agentgo/internal/userhooks"
	"github.com/cloudwego/eino/components/tool/utils"
)

// A local model-protocol peer exercises the production Runner, not a substitute
// turn implementation. No remote model or desktop host is involved.
type turnProtocolPeer struct {
	calls    atomic.Int32
	mu       sync.Mutex
	inputs   []string
	tool     string
	fallback bool
}

func (p *turnProtocolPeer) serve(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var in struct {
		Stream   bool
		Messages []struct {
			Role    string
			Content json.RawMessage
		}
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.calls.Add(1)
	hasTool := false
	p.mu.Lock()
	for _, m := range in.Messages {
		if m.Role == "user" {
			p.inputs = append(p.inputs, string(m.Content))
		}
		if m.Role == "tool" {
			hasTool = true
		}
	}
	p.mu.Unlock()
	if p.fallback && in.Stream {
		http.Error(w, "streaming tool execution rejected", http.StatusBadRequest)
		return
	}
	message := map[string]any{"role": "assistant", "content": "checked artifact"}
	finish := "stop"
	if p.tool != "" && !hasTool {
		message["content"] = ""
		message["tool_calls"] = []any{map[string]any{"index": 0, "id": "turn-call", "type": "function", "function": map[string]string{"name": p.tool, "arguments": `{"path":"fixture.txt"}`}}}
		finish = "tool_calls"
	}
	usage := map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		raw, _ := json.Marshal(map[string]any{"id": "turn-test", "object": "chat.completion.chunk", "created": 1, "model": "turn-test", "choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}, "usage": usage})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	} else {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "turn-test", "object": "chat.completion", "created": 1, "model": "turn-test", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
	}
}

func turnProtocolRuntime(t *testing.T, peer *turnProtocolPeer) *Runtime {
	t.Helper()
	t.Setenv("AGENTGO_LLM_API", "chat")
	t.Setenv("AGENTGO_AUTO_JOURNAL", "0")
	srv := httptest.NewServer(http.HandlerFunc(peer.serve))
	t.Cleanup(srv.Close)
	rt := boot(t)
	if err := rt.SetLLMConfig(LLMConfig{APIBase: srv.URL + "/v1", APIKey: "local-test", Model: "turn-test"}); err != nil {
		t.Fatal(err)
	}
	h, _ := agent.ResolveHarness("standard")
	rt.agentRunner.SetSessionMode(h.Mode())
	return rt
}

func assertTurnStored(t *testing.T, rt *Runtime, sid, reply string) {
	t.Helper()
	rows, err := rt.Sessions().GetMessages(context.Background(), sid, 100)
	if err != nil {
		t.Fatal(err)
	}
	users, assistants := 0, 0
	for _, row := range rows {
		if row.Role == "user" {
			users++
		}
		if row.Role == "assistant" && row.Type == "text" {
			assistants++
			if row.Content != reply {
				t.Fatalf("stored answer differs: %q", row.Content)
			}
		}
	}
	if users != 1 || assistants != 1 {
		t.Fatalf("duplicate/missing transcript: users=%d assistants=%d", users, assistants)
	}
	state, err := rt.Sessions().LastRunState(context.Background(), sid)
	if err != nil || state.Status != sessions.RunCompleted {
		t.Fatalf("uncommitted turn: %+v %v", state, err)
	}
}

func TestRuntimeTurnSharesContextEvidenceAndCompletionAcrossTransports(t *testing.T) {
	p := &turnProtocolPeer{tool: "code_view"}
	rt := turnProtocolRuntime(t, p)
	if err := os.WriteFile(filepath.Join(rt.WorkspaceRoot(), "fixture.txt"), []byte("artifact fact"), 0600); err != nil {
		t.Fatal(err)
	}
	var executed atomic.Int32
	reader, err := utils.InferTool("code_view", "read fixture", func(ctx context.Context, args struct {
		Path string `json:"path"`
	}) (string, error) {
		executed.Add(1)
		sid := agent.SessionIDFromContext(ctx)
		if governance.SessionIDFromContext(ctx) != sid {
			t.Error("split run scope")
		}
		c := evidence.Register(ctx, sid, evidence.Citation{Kind: "file", Source: args.Path, Revision: "r1", End: 13}, "artifact fact")
		if c == nil {
			return "", errors.New("missing evidence recorder")
		}
		return "artifact fact", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.toolReg.AddTool(reader)
	hookGroups := map[string]any{}
	for event, command := range map[string]string{userhooks.UserPromptSubmit: echoLine("hook-context-token"), userhooks.Stop: echoLine("stopped")} {
		hookGroups[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}}}
	}
	rawHooks, err := json.Marshal(map[string]any{"hooks": hookGroups})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.dataDir, userHooksFile), rawHooks, 0600); err != nil {
		t.Fatal(err)
	}
	rt.hooksEngine().Reload()
	g := NewRuntimeGateway(rt, nil) // No AppService has been constructed.
	interrupts := 0
	result, err := g.StreamChat(context.Background(), gateway.ChatRequest{SessionID: "gateway-turn", Message: "/review @fixture.txt"}, func(event string, _ []byte) error {
		if event == "interrupt" {
			interrupts++
		}
		return nil
	})
	if err != nil || result.Error != "" || result.Content != "checked artifact" || interrupts != 0 || result.Usage.ToolCalls != 1 {
		t.Fatalf("gateway result: %+v %v", result, err)
	}
	assertTurnStored(t, rt, "gateway-turn", result.Content)
	svc := &AppService{rt: rt}
	done := 0
	desktop := svc.RunEngineSync(EinoEngineID, "desktop-turn", "/review @fixture.txt", nil, engine.SinkFunc(func(ev engine.Event) {
		if ev.Type == engine.EventDone {
			done++
			assertTurnStored(t, rt, "desktop-turn", "checked artifact")
		}
	}))
	if desktop.Error != "" || desktop.Pending || done != 1 {
		t.Fatalf("desktop result: %+v done=%d", desktop, done)
	}
	legacy := svc.SendMessageWithSession("legacy-turn", "/review @fixture.txt", nil)
	if legacy.Error != "" {
		t.Fatal(legacy.Error)
	}
	assertTurnStored(t, rt, "legacy-turn", "checked artifact")
	svc.runStream("stream-turn", "/review @fixture.txt", nil)
	assertTurnStored(t, rt, "stream-turn", "checked artifact")
	stopHooksWG.Wait()
	if executed.Load() != 4 {
		t.Fatalf("tool executions=%d", executed.Load())
	}
	for _, sid := range []string{"gateway-turn", "desktop-turn", "legacy-turn", "stream-turn"} {
		rows, _ := rt.Sessions().GetMessages(context.Background(), sid, 100)
		n := 0
		for _, row := range rows {
			if row.Type == "source_evidence" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s evidence records=%d", sid, n)
		}
	}
	p.mu.Lock()
	inputs := append([]string(nil), p.inputs...)
	p.mu.Unlock()
	for _, input := range inputs {
		for _, want := range []string{"artifact fact", "hook-context-token", "get_workspace_changes"} {
			if !strings.Contains(input, want) {
				t.Fatalf("model input missing %q: %.300s", want, input)
			}
		}
	}
	counts := map[string]int{}
	for _, run := range rt.hooksEngine().Status().Recent {
		counts[run.Event]++
	}
	if counts[string(userhooks.UserPromptSubmit)] != 4 || counts[string(userhooks.Stop)] != 4 {
		t.Fatalf("duplicate/missing turn hooks: %v", counts)
	}
}

func TestRuntimeTurnDenialNeverCallsModelAcrossTransports(t *testing.T) {
	p := &turnProtocolPeer{}
	rt := turnProtocolRuntime(t, p)
	writeUserHooks(t, rt, userhooks.UserPromptSubmit, "", refuseWith("blocked by fixture"))
	g := NewRuntimeGateway(rt, nil)
	res, err := g.StreamChat(context.Background(), gateway.ChatRequest{SessionID: "denied-gateway", Message: "deploy"}, nil)
	if err != nil || res.Error != "" || !strings.Contains(res.Content, "blocked by fixture") {
		t.Fatalf("%+v %v", res, err)
	}
	svc := &AppService{rt: rt}
	desktop := svc.RunEngineSync(EinoEngineID, "denied-desktop", "deploy", nil, nil)
	if desktop.Error != "" || len(desktop.Messages) != 1 || !strings.Contains(desktop.Messages[0].Content, "blocked by fixture") {
		t.Fatalf("%+v", desktop)
	}
	if p.calls.Load() != 0 {
		t.Fatalf("denied turn called model %d times", p.calls.Load())
	}
}

func TestRuntimeTurnGatewayApprovalCanResumeWithoutDesktopHost(t *testing.T) {
	p := &turnProtocolPeer{tool: "code_view"}
	rt := turnProtocolRuntime(t, p)
	policy := rt.agentRunner.EffectivePolicy()
	policy.ToolRiskLevels["code_view"] = governance.RiskHigh
	rt.agentRunner.SetPolicy(policy)
	var executed atomic.Int32
	action, err := utils.InferTool("code_view", "approved action", func(context.Context, struct {
		Path string `json:"path"`
	}) (string, error) {
		executed.Add(1)
		return "approved artifact", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.toolReg.AddTool(action)
	g := NewRuntimeGateway(rt, nil)
	approval := ""
	res, err := g.StreamChat(context.Background(), gateway.ChatRequest{SessionID: "approval-gateway", Message: "inspect"}, func(event string, raw []byte) error {
		if event == "interrupt" {
			var payload struct {
				ApprovalID string `json:"approval_id"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Error(err)
			}
			approval = payload.ApprovalID
			state, _ := rt.Sessions().LastRunState(context.Background(), "approval-gateway")
			if state.Status != sessions.RunAwaitingApproval {
				t.Error("interrupt preceded pending commit", state)
			}
			request, err := rt.Approvals().GetRequest(context.Background(), payload.ApprovalID)
			if err != nil || request == nil || request.Metadata["execution_id"] != state.RunID {
				t.Error("governance invented a separate root from the native run", request, state, err)
			}
		}
		return nil
	})
	if err != nil || res.Error != "" || approval == "" || executed.Load() != 0 {
		t.Fatalf("did not pause: %+v id=%q calls=%d %v", res, approval, executed.Load(), err)
	}
	res, err = g.ResolveApproval(context.Background(), approval, true, func(string, []byte) error { return nil })
	if err != nil || res.Error != "" || res.Content != "checked artifact" || executed.Load() != 1 {
		t.Fatalf("resume: %+v calls=%d %v", res, executed.Load(), err)
	}
	assertTurnStored(t, rt, "approval-gateway", "checked artifact")
	if _, err := g.ResolveApproval(context.Background(), approval, true, func(string, []byte) error { return nil }); err == nil || executed.Load() != 1 {
		t.Fatal("approval replay executed again")
	}
}

type disconnectTurnEngine struct{ ended chan struct{} }

func (disconnectTurnEngine) ID() string { return EinoEngineID }
func (e disconnectTurnEngine) Run(ctx context.Context, _ engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	em.Token("partial")
	<-ctx.Done()
	close(e.ended)
	return engine.RunResult{}, ctx.Err()
}

func TestGatewayTransportFailureCancelsTheRuntimeRun(t *testing.T) {
	rt := boot(t)
	ended := make(chan struct{})
	rt.engineRouter.Register(disconnectTurnEngine{ended: ended})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	disconnected := errors.New("client disconnected")
	_, err := NewRuntimeGateway(rt, nil).StreamChat(ctx, gateway.ChatRequest{SessionID: "disconnect", Message: "go"}, func(string, []byte) error { return disconnected })
	if !errors.Is(err, disconnected) {
		t.Fatalf("transport error lost: %v", err)
	}
	select {
	case <-ended:
	default:
		t.Fatal("native run still active")
	}
	if len(rt.engineRouter.Active()) != 0 {
		t.Fatal("router retained cancelled run")
	}
}

func TestNativeTurnCancellationDoesNotStartFallbackAcrossTransports(t *testing.T) {
	for _, surface := range []string{"gateway", "desktop"} {
		t.Run(surface, func(t *testing.T) {
			p := &turnProtocolPeer{tool: "code_view"}
			rt := turnProtocolRuntime(t, p)
			entered := make(chan struct{})
			var executed atomic.Int32
			action, err := utils.InferTool("code_view", "cancel fixture", func(ctx context.Context, _ struct {
				Path string `json:"path"`
			}) (string, error) {
				executed.Add(1)
				close(entered)
				<-ctx.Done()
				return "", ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			rt.toolReg.AddTool(action)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan SendMessageResult, 1)
			g, svc := NewRuntimeGateway(rt, nil), &AppService{rt: rt}
			go func() {
				if surface == "gateway" {
					res, err := g.StreamChat(ctx, gateway.ChatRequest{SessionID: "cancel-turn", Message: "inspect"}, nil)
					if err != nil {
						done <- SendMessageResult{Error: err.Error()}
						return
					}
					done <- SendMessageResult{Error: res.Error, Degraded: res.Degraded}
				} else {
					done <- sendResultFromEngine(svc.runEngineTurn(ctx, engine.RunRequest{Engine: EinoEngineID, SessionID: "cancel-turn", Input: "inspect"}, nil))
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("tool did not start")
			}
			if surface == "gateway" {
				if !g.CancelSessionRun(ctx, "cancel-turn") {
					t.Fatal("gateway cancel missed the run")
				}
			} else if res := svc.StopSession("cancel-turn"); res["cancelled"] != true {
				t.Fatal(res)
			}
			select {
			case res := <-done:
				if res.Error == "" || res.Degraded != "" || p.calls.Load() != 1 || executed.Load() != 1 {
					t.Fatalf("cancel became fallback/success: %+v requests=%d executions=%d", res, p.calls.Load(), executed.Load())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancel did not finish")
			}
			if len(rt.engineRouter.Active()) != 0 {
				t.Fatal("stale active run")
			}
		})
	}
}

func TestRuntimeFallbackStaysMarkedAcrossGatewayAndRunLog(t *testing.T) {
	p := &turnProtocolPeer{fallback: true}
	rt := turnProtocolRuntime(t, p)
	res, err := NewRuntimeGateway(rt, nil).StreamChat(context.Background(), gateway.ChatRequest{SessionID: "fallback", Message: "inspect"}, nil)
	if err != nil || res.Error != "" || res.Degraded == "" || !strings.HasPrefix(res.Content, "[能力降级]") {
		t.Fatalf("unmarked fallback: %+v %v", res, err)
	}
	assertTurnStored(t, rt, "fallback", res.Content)
	rows, _ := rt.Sessions().ListRunEvents(context.Background(), "fallback", 0, 0)
	if len(rows) == 0 || !strings.Contains(rows[len(rows)-1].PayloadJSON, "\"degraded\"") {
		t.Fatal("durable result lost fallback marker")
	}
}

func TestRapidTurnsDoNotOverwriteEachOthersMemoryRecord(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 1000; i++ {
		record := memoryRecordFromTurn("request", "verified answer")
		if ids[record.ID] {
			t.Fatal("different turns share a memory primary key", record.ID)
		}
		ids[record.ID] = true
	}
}
