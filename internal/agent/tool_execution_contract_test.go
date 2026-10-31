package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

func TestHealingNeverRetriesEndpointSideEffects(t *testing.T) {
	for _, asError := range []bool{false, true} {
		calls := 0
		fn := wrapHealInvokableEndpoint(func(context.Context, string, ...tool.Option) (string, error) {
			calls++
			if asError {
				return "", errors.New("json: validation error after side effect")
			}
			return "validation error after side effect", nil
		}, nil)
		_, _ = fn(context.Background(), `{"path":"target"}`)
		if calls != 1 {
			t.Fatal("endpoint was implicitly retried", calls)
		}
	}
	calls := 0
	mw := ComposeToolHealMiddleware()
	fn := mw.Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		calls++
		return nil, errors.New("validation error")
	})
	_, _ = fn(context.Background(), &compose.ToolInput{Name: "demo", Arguments: `{"path":"target"}`})
	if calls != 1 {
		t.Fatal("Compose endpoint was implicitly retried", calls)
	}
}

func TestHealingPreservesValidArgumentSemantics(t *testing.T) {
	for _, raw := range []string{
		`{"arguments":{"name":"literal"},"args_json":"{\"key\":1}"}`,
		`{"arguments":{"name":"literal"}}`,
	} {
		got, err := prepareToolArguments(raw)
		if err != nil || got != raw {
			t.Fatalf("valid arguments were rewritten: %q -> %q (%v)", raw, got, err)
		}
	}
	raw := "{\"text\":\"literal,} content\",\n}"
	got, err := prepareToolArguments(raw)
	if err != nil || !strings.Contains(got, "literal,} content") {
		t.Fatal("comma repair changed quoted content", got, err)
	}
}

func TestInvalidArgumentsNeverReachTool(t *testing.T) {
	called := false
	fn := wrapHealInvokableEndpoint(func(context.Context, string, ...tool.Option) (string, error) { called = true; return "done", nil }, nil)
	if _, err := fn(context.Background(), "not-json"); err == nil || called {
		t.Fatal("invalid arguments executed", err, called)
	}
}

func TestSafeToolPreservesCancellation(t *testing.T) {
	mw := NewSafeToolMiddleware()
	fn, err := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) { return "", context.Canceled }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fn(context.Background(), "{}"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was converted to success", err)
	}
}
