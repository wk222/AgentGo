package governance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// Hooks are tested through the same chain the app runs: ChatModelAgent +
// ComposeToolMiddleware, not by calling the middleware directly.

const freeTool = "code_view" // Low in the risk table: runs without approval

type hookRig struct {
	runs   int32
	queue  *ApprovalQueue
	store  *memCheckpointStore
	policy Policy
}

func newHookRig(t *testing.T, mode string) *hookRig {
	t.Helper()
	q, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return &hookRig{queue: q, store: &memCheckpointStore{}, policy: BuildPolicy(mode, "/ws/project")}
}

func (r *hookRig) runner(t *testing.T, toolName string) *adk.Runner {
	t.Helper()
	agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name: "test", Description: "test", Model: scriptedModel{tool: toolName},
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools:               []tool.BaseTool{countingTool{runs: &r.runs, name: toolName}},
			ToolCallMiddlewares: []compose.ToolMiddleware{ComposeToolMiddleware(r.queue, r.policy)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: agent, CheckPointStore: r.store})
}

func (r *hookRig) run(t *testing.T, toolName string) drained {
	t.Helper()
	ctx := WithSessionID(context.Background(), "sess-1")
	return drainAgent(t, r.runner(t, toolName).Run(ctx, []adk.Message{schema.UserMessage("go")}, adk.WithCheckPointID("cp")))
}

type hookLog struct {
	mu     sync.Mutex
	events []string
	calls  []ToolCall
	out    []ToolOutcome
}

func (l *hookLog) add(s string) { l.mu.Lock(); l.events = append(l.events, s); l.mu.Unlock() }
func (l *hookLog) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.events, ",")
}

func recordingHook(name string, l *hookLog, veto error) ToolHook {
	return ToolHook{
		Name: name,
		Before: func(_ context.Context, c ToolCall) error {
			l.mu.Lock()
			l.calls = append(l.calls, c)
			l.mu.Unlock()
			l.add(name + ".before")
			return veto
		},
		After: func(_ context.Context, _ ToolCall, o ToolOutcome) {
			l.mu.Lock()
			l.out = append(l.out, o)
			l.mu.Unlock()
			l.add(name + ".after")
		},
	}
}

func TestToolHooksSeeTheRealCallInOrder(t *testing.T) {
	r := newHookRig(t, "balanced")
	var l hookLog
	t.Cleanup(RegisterToolHook(recordingHook("one", &l, nil)))
	t.Cleanup(RegisterToolHook(recordingHook("two", &l, nil)))

	res := r.run(t, freeTool)

	if got := atomic.LoadInt32(&r.runs); got != 1 {
		t.Fatalf("tool ran %d times", got)
	}
	if want := "one.before,two.before,one.after,two.after"; l.joined() != want {
		t.Fatalf("order = %s, want %s", l.joined(), want)
	}
	c := l.calls[0]
	if c.Name != freeTool || c.CallID != "call_1" || c.SessionID != "sess-1" || c.Workspace != "/ws/project" || !strings.Contains(c.Arguments, "a.txt") {
		t.Fatalf("hook saw %+v", c)
	}
	if o := l.out[0]; o.Err != nil || o.Output != "tool_ok" || o.Duration < 0 {
		t.Fatalf("outcome = %+v", o)
	}
	if res.text != "done" {
		t.Fatalf("answer = %q", res.text)
	}
}

func TestToolHookVetoStopsTheCallAndTheModelIsTold(t *testing.T) {
	r := newHookRig(t, "balanced")
	var first, second hookLog
	t.Cleanup(RegisterToolHook(recordingHook("guard", &first, errors.New("not on my watch"))))
	t.Cleanup(RegisterToolHook(recordingHook("later", &second, nil)))

	res := r.run(t, freeTool)

	if got := atomic.LoadInt32(&r.runs); got != 0 {
		t.Fatalf("vetoed tool ran %d times", got)
	}
	if first.joined() != "guard.before" {
		t.Fatalf("vetoing hook events = %s (After must not run for a vetoed call)", first.joined())
	}
	if second.joined() != "" {
		t.Fatalf("later hooks must be skipped after a veto, got %s", second.joined())
	}
	if res.text != "done" {
		t.Fatalf("the run should continue with the rejection as the tool result, got %q", res.text)
	}
}

func TestToolHookInterceptionEndsWhenUnregistered(t *testing.T) {
	r := newHookRig(t, "balanced")
	var l hookLog
	dispose := RegisterToolHook(recordingHook("guard", &l, errors.New("no")))

	r.run(t, freeTool)
	if atomic.LoadInt32(&r.runs) != 0 {
		t.Fatal("vetoed while registered")
	}
	dispose()
	r.run(t, freeTool)
	if atomic.LoadInt32(&r.runs) != 1 {
		t.Fatal("still intercepted after unregistering")
	}
}

// A call waiting for approval is not executing, so hooks must not see it until
// the user approves; a rejected call never reaches them at all.
func TestToolHooksOnlySeeCallsThatActuallyRun(t *testing.T) {
	for _, approve := range []bool{true, false} {
		r := newHookRig(t, "balanced")
		var l hookLog
		t.Cleanup(RegisterToolHook(recordingHook("audit", &l, nil)))

		first := r.run(t, "create_tool") // High risk: pauses for approval
		if first.interruptID == "" {
			t.Fatal("expected a pause for approval")
		}
		if l.joined() != "" {
			t.Fatalf("hooks ran while the call was only waiting for approval: %s", l.joined())
		}
		ctx := context.Background()
		pending, err := r.queue.ListPending(ctx, nil)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending=%d err=%v", len(pending), err)
		}
		if err := r.queue.Resolve(ctx, pending[0].ID, approve, "t", "tester", &ResumePayload{Approved: approve}); err != nil {
			t.Fatal(err)
		}
		iter, err := r.runner(t, "create_tool").ResumeWithParams(ctx, "cp", &adk.ResumeParams{
			Targets: map[string]any{first.interruptID: &ResumePayload{Approved: approve}},
		})
		if err != nil {
			t.Fatal(err)
		}
		drainAgent(t, iter)

		want, wantRuns := "", int32(0)
		if approve {
			want, wantRuns = "audit.before,audit.after", 1
		}
		if l.joined() != want || atomic.LoadInt32(&r.runs) != wantRuns {
			t.Fatalf("approve=%v: hook events=%q runs=%d, want %q / %d", approve, l.joined(), r.runs, want, wantRuns)
		}
	}
}

func TestToolHookPanicsAreContained(t *testing.T) {
	r := newHookRig(t, "balanced")
	t.Cleanup(RegisterToolHook(ToolHook{Name: "boom-after", After: func(context.Context, ToolCall, ToolOutcome) { panic("after") }}))
	r.run(t, freeTool)
	if atomic.LoadInt32(&r.runs) != 1 {
		t.Fatal("a panicking After hook must not affect the call")
	}

	dispose := RegisterToolHook(ToolHook{Name: "boom-before", Before: func(context.Context, ToolCall) error { panic("before") }})
	defer dispose()
	r.run(t, freeTool)
	if atomic.LoadInt32(&r.runs) != 1 {
		t.Fatal("a panicking Before hook must veto (fail closed)")
	}
}

func TestToolCallWithCancelledContextDoesNotRun(t *testing.T) {
	var l hookLog
	t.Cleanup(RegisterToolHook(recordingHook("audit", &l, nil)))
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	mw := NewGovernanceMiddleware(queue, BuildPolicy("balanced", ""))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	_, err = mw.InvokeWithPolicy(ctx, freeTool, `{}`, func(context.Context, string) (string, error) { ran = true; return "x", nil })
	if ran || !errors.Is(err, context.Canceled) {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if l.joined() != "" {
		t.Fatalf("hooks saw a cancelled call: %s", l.joined())
	}
}
