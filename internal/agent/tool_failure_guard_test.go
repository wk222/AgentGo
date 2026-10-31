package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

func wrapSafe(t *testing.T, name string, ep adk.InvokableToolCallEndpoint) adk.InvokableToolCallEndpoint {
	t.Helper()
	w, err := NewSafeToolMiddleware().WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSameFailureGetsOneHintThenARefusalAndASuccessResets(t *testing.T) {
	calls := 0
	fail := true
	w := wrapSafe(t, "write_file", func(ctx context.Context, args string, _ ...tool.Option) (string, error) {
		calls++
		if fail {
			return "", errors.New("permission denied")
		}
		return `{"success":true}`, nil
	})
	ctx := context.Background()
	args := `{"path":"a","content":"x"}`
	var outs []string
	for i := 0; i < 7; i++ {
		out, err := w(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, out)
	}
	hints := 0
	for _, o := range outs {
		if strings.Contains(o, "[hint]") {
			hints++
		}
	}
	if hints != 1 || !strings.Contains(outs[2], "[hint]") {
		t.Fatalf("exactly one hint, at the third failure: %q", outs)
	}
	if !strings.HasPrefix(outs[6], "[stop-loss]") {
		t.Fatalf("seventh call should be refused: %q", outs[6])
	}
	if calls != 6 {
		t.Fatalf("the tool must not run once refused; ran %d times", calls)
	}

	// 参数不同：不受影响，也不计入原来的次数。
	fail = false
	if out, _ := w(ctx, `{"path":"b","content":"x"}`); !strings.Contains(out, "success") {
		t.Fatalf("different arguments must run: %q", out)
	}
}

func TestSuccessInBetweenResetsTheCount(t *testing.T) {
	fail := true
	w := wrapSafe(t, "t", func(ctx context.Context, args string, _ ...tool.Option) (string, error) {
		if fail {
			return `{"success":false,"error":"nope"}`, nil
		}
		return `{"success":true}`, nil
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = w(ctx, `{"a":1}`)
	}
	fail = false
	_, _ = w(ctx, `{"a":1}`)
	fail = true
	for i := 0; i < 2; i++ {
		if out, _ := w(ctx, `{"a":1}`); strings.Contains(out, "[hint]") {
			t.Fatalf("count must restart after a success: %q", out)
		}
	}
}

func TestArgumentOrderDoesNotHideARepeat(t *testing.T) {
	if failureKey("t", `{"a":1,"b":2}`) != failureKey("t", `{"b":2,"a":1}`) {
		t.Fatal("the same arguments in another key order are the same call")
	}
	if failureKey("t", `{"a":1}`) == failureKey("u", `{"a":1}`) {
		t.Fatal("different tools are different calls")
	}
}

func TestOutcomeUnknownIsMarkedOnlyWhenTheRequestMayHaveLeft(t *testing.T) {
	for _, err := range []error{io.ErrUnexpectedEOF, fmt.Errorf("post: %w", io.EOF), errors.New("read tcp: connection reset by peer"),
		errors.New("Post \"https://x\": context deadline exceeded (Client.Timeout exceeded)"), errors.New("command timed out after 30s")} {
		if got := failureText(err); !strings.Contains(got, "outcome unknown") {
			t.Errorf("%v should be marked unknown: %q", err, got)
		}
	}
	for _, err := range []error{errors.New("tool arguments must be a valid JSON object; execution was not started"),
		errors.New("file not found"), errors.New("timeout must be at least 5")} {
		if got := failureText(err); strings.Contains(got, "outcome unknown") {
			t.Errorf("%v must not be marked unknown: %q", err, got)
		}
	}
}

func TestResultFailed(t *testing.T) {
	for s, want := range map[string]bool{
		`[tool error] x`: true, `{"success":false}`: true, `{"error":"boom"}`: true,
		`{"success":true}`: false, `{"error":""}`: false, `plain text`: false, ``: false, `{"ok":1}`: false,
	} {
		if got := resultFailed(s); got != want {
			t.Errorf("resultFailed(%q) = %v, want %v", s, got, want)
		}
	}
}
