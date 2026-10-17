package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

type recObs struct{ lines []string }

func (o *recObs) ToolStarted(id, name, args string) {
	o.lines = append(o.lines, "start "+id+" "+name+" "+args)
}

func (o *recObs) ToolFinished(id, name, out string, isErr bool) {
	s := "end " + id + " " + name + " " + out
	if isErr {
		s += " ERR"
	}
	o.lines = append(o.lines, s)
}

func wrapAndRun(t *testing.T, ctx context.Context, fn adk.InvokableToolCallEndpoint) (string, error) {
	t.Helper()
	ep, err := NewToolCallEventMiddleware().WrapInvokableToolCall(ctx, fn, &adk.ToolContext{Name: "view", CallID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	return ep(ctx, `{"a":1}`)
}

func TestToolObserverSeesSuccessAndFailure(t *testing.T) {
	obs := &recObs{}
	ctx := WithToolObserver(context.Background(), obs)

	out, err := wrapAndRun(t, ctx, func(context.Context, string, ...tool.Option) (string, error) { return "ok", nil })
	if err != nil || out != "ok" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	_, err = wrapAndRun(t, ctx, func(context.Context, string, ...tool.Option) (string, error) { return "", errors.New("bad") })
	if err == nil {
		t.Fatal("error must propagate unchanged")
	}
	want := []string{`start c1 view {"a":1}`, "end c1 view ok", `start c1 view {"a":1}`, "end c1 view bad ERR"}
	if len(obs.lines) != len(want) {
		t.Fatalf("got %q", obs.lines)
	}
	for i := range want {
		if obs.lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, obs.lines[i], want[i])
		}
	}
}

// ADK passes an empty CallID to the middleware; start and finish must still
// carry the same non-empty id or frontends cannot pair a call with its result.
func TestToolObserverGeneratesPairedIDWhenFrameworkHasNone(t *testing.T) {
	obs := &recObs{}
	ctx := WithToolObserver(context.Background(), obs)
	ep, err := NewToolCallEventMiddleware().WrapInvokableToolCall(ctx,
		func(context.Context, string, ...tool.Option) (string, error) { return "ok", nil },
		&adk.ToolContext{Name: "view"}) // no CallID
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ep(ctx, "{}"); err != nil {
		t.Fatal(err)
	}
	if len(obs.lines) != 2 {
		t.Fatalf("lines = %q", obs.lines)
	}
	var s, e string
	if _, err := fmt.Sscanf(obs.lines[0], "start %s", &s); err != nil || s == "" || s == "view" {
		t.Fatalf("start line = %q", obs.lines[0])
	}
	if _, err := fmt.Sscanf(obs.lines[1], "end %s", &e); err != nil || e != s {
		t.Fatalf("end id %q != start id %q (%q)", e, s, obs.lines[1])
	}
}

func TestToolWithoutObserverIsUnaffected(t *testing.T) {
	out, err := wrapAndRun(t, context.Background(), func(context.Context, string, ...tool.Option) (string, error) { return "ok", nil })
	if err != nil || out != "ok" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if WithToolObserver(context.Background(), nil) == nil {
		t.Fatal("nil observer must return ctx unchanged")
	}
}

// ask_user stops the run with an interrupt signal. That is a pause for the
// user, not a failed tool, so frontends must not see an error result.
func TestToolObserverReportsInterruptAsPauseNotFailure(t *testing.T) {
	obs := &recObs{}
	ctx := WithToolObserver(context.Background(), obs)
	// The real thing: tool.Interrupt's signal, wrapped with %w as the tool adapters do.
	sig := fmt.Errorf("[LocalFunc] failed to invoke tool, toolName=ask_user, err=%w", compose.Interrupt(ctx, "q"))
	_, err := wrapAndRun(t, ctx, func(context.Context, string, ...tool.Option) (string, error) { return "", sig })
	if err == nil {
		t.Fatal("the interrupt must still propagate so the run pauses")
	}
	if len(obs.lines) != 2 || strings.HasSuffix(obs.lines[1], "ERR") {
		t.Fatalf("pause reported as failure: %q", obs.lines)
	}
}
