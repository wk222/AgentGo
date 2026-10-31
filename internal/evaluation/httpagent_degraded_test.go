package evaluation

import (
	"strings"
	"testing"
)

func TestHTTPAgentRejectsToollessFallbackAsTaskSuccess(t *testing.T) {
	stream := "event: chunk\ndata: {\"delta\":\"partial unverified text\"}\n\nevent: done\ndata: {\"content\":\"[能力降级] fallback\",\"degraded\":\"tool execution failed\",\"usage\":{\"model_calls\":2,\"tool_calls\":1}}\n\n"
	out, metrics, err := parseChatSSE(strings.NewReader(stream))
	if err == nil || !strings.Contains(err.Error(), "agent degraded") || out != "[能力降级] fallback" || metrics.ModelCalls != 2 || metrics.ToolCalls != 1 {
		t.Fatalf("fallback counted as completed task: out=%q metrics=%+v err=%v", out, metrics, err)
	}
}
