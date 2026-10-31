package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// transientErrorMarks are lowercase fragments of errors that a second attempt
// usually fixes: rate limits, gateway failures, and connections or HTTP/2
// streams the peer dropped.
var transientErrorMarks = []string{
	"429", "too many requests", "rate limit", "qpm limit",
	"timeout",
	"502", "503", "504", "bad gateway", "service unavailable", "gateway time-out",
	"connection reset", "connection closed", "broken pipe", "unexpected eof",
	"goaway", "internal_error", "stream error",
}

func isRetryableAPIError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, mark := range transientErrorMarks {
		if strings.Contains(msg, mark) {
			return true
		}
	}
	return false
}

// leakedToolMarkup is the fullwidth-bar tag DeepSeek models use for their own
// tool-call syntax. When a relay's parser fails to turn a call into structured
// tool_calls, the tail of that syntax arrives as ordinary reply text.
const leakedToolMarkup = "\uFF5CDSML\uFF5C"

// hasLeakedToolMarkup reports a reply whose tool call never became a tool call:
// no structured calls, but DeepSeek's call syntax sits in the text. The turn
// would end with garbage and no action, so it is better re-asked.
func hasLeakedToolMarkup(m *schema.Message) bool {
	return m != nil && len(m.ToolCalls) == 0 && strings.Contains(m.Content, leakedToolMarkup)
}

// willRetry reports the marker the ADK retry wrapper puts on a failed model
// attempt it is about to repeat. Consumers must skip it, not fail on it.
func willRetry(err error) bool {
	var marker *adk.WillRetryError
	return errors.As(err, &marker)
}

// shouldRetryModelCall decides whether one model attempt is retried: transient
// transport errors, and replies that carry leaked tool-call syntax.
func shouldRetryModelCall(_ context.Context, rc *adk.RetryContext) *adk.RetryDecision {
	switch {
	case rc.Err != nil:
		return &adk.RetryDecision{Retry: isRetryableAPIError(rc.Err), RejectReason: "transient_error"}
	case hasLeakedToolMarkup(rc.OutputMessage):
		return &adk.RetryDecision{Retry: true, RejectReason: "leaked_tool_markup"}
	}
	return nil
}

// DefaultModelRetryConfig retries rate limits, gateway failures and dropped
// connections or streams, and replies with leaked tool-call syntax.
func DefaultModelRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries:  5,
		ShouldRetry: shouldRetryModelCall,
	}
}

// DefaultTypedModelRetryConfig is the AgenticMessage variant of DefaultModelRetryConfig.
func DefaultTypedModelRetryConfig[M adk.MessageType]() *adk.TypedModelRetryConfig[M] {
	return &adk.TypedModelRetryConfig[M]{
		MaxRetries:  5,
		IsRetryAble: func(_ context.Context, err error) bool { return isRetryableAPIError(err) },
	}
}
