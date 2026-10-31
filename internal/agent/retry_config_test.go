package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestIsRetryableAPIError(t *testing.T) {
	retry := []string{
		"error, status code: 502, status: 502 Bad Gateway",
		"failed to receive stream chunk: stream error: stream ID 3; INTERNAL_ERROR; received from peer",
		"error, status code: 429, message: rate limit",
		"read tcp: connection reset by peer",
		"http2: server sent GOAWAY and closed the connection",
		"unexpected EOF",
		"error, status code: 504",
	}
	for _, m := range retry {
		if !isRetryableAPIError(errors.New(m)) {
			t.Errorf("%q must be retryable", m)
		}
	}
	for _, m := range []string{
		"error, status code: 401, message: API key 无效",
		"error, status code: 404 Not Found, message: unexpected end of JSON input",
		"error, status code: 400, message: 未知模型",
	} {
		if isRetryableAPIError(errors.New(m)) {
			t.Errorf("%q must not be retried", m)
		}
	}
	if isRetryableAPIError(nil) {
		t.Error("nil is not an error")
	}
}

func TestShouldRetryModelCall(t *testing.T) {
	ctx := context.Background()
	tail := "</\uFF5CDSML\uFF5Cparameter>\n</\uFF5CDSML\uFF5Cinvoke>\n</\uFF5CDSML\uFF5Ctool_calls>"
	cases := []struct {
		name string
		rc   adk.RetryContext
		want bool
	}{
		{"leaked syntax, no structured call", adk.RetryContext{OutputMessage: &schema.Message{Content: tail}}, true},
		{"leaked syntax but a real call exists", adk.RetryContext{OutputMessage: &schema.Message{Content: tail, ToolCalls: []schema.ToolCall{{ID: "1"}}}}, false},
		{"normal reply", adk.RetryContext{OutputMessage: &schema.Message{Content: "你好"}}, false},
		{"transient error", adk.RetryContext{Err: errors.New("stream error: INTERNAL_ERROR")}, true},
		{"transient error with partial output", adk.RetryContext{Err: errors.New("502 Bad Gateway"), OutputMessage: &schema.Message{Content: "半句"}}, true},
		{"permanent error", adk.RetryContext{Err: errors.New("status code: 401")}, false},
		{"empty stream", adk.RetryContext{}, false},
	}
	for _, c := range cases {
		d := shouldRetryModelCall(ctx, &c.rc)
		got := d != nil && d.Retry
		if got != c.want {
			t.Errorf("%s: retry=%v, want %v", c.name, got, c.want)
		}
	}
}
