package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestSafeToolMiddlewareConvertsError(t *testing.T) {
	mw := NewSafeToolMiddleware()
	endpoint := func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		return "", errors.New("file not found")
	}
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if out == "" || out[:12] != "[tool error]" {
		t.Fatalf("unexpected output: %q", out)
	}
}
