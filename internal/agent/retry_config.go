package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
)

func isRetryableAPIError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "qpm limit") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "502")
}

// DefaultModelRetryConfig matches Eino Ch.05 (429 / rate limit / transient network).
func DefaultModelRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries:  5,
		IsRetryAble: func(_ context.Context, err error) bool { return isRetryableAPIError(err) },
	}
}

// DefaultTypedModelRetryConfig is the AgenticMessage variant of DefaultModelRetryConfig.
func DefaultTypedModelRetryConfig[M adk.MessageType]() *adk.TypedModelRetryConfig[M] {
	return &adk.TypedModelRetryConfig[M]{
		MaxRetries:  5,
		IsRetryAble: func(_ context.Context, err error) bool { return isRetryableAPIError(err) },
	}
}
