package agent

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Live check against a real Responses endpoint. Opt-in:
//
//	$env:AGENTGO_LIVE_RESPONSES = "1"   # uses AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT
//	go test -run TestResponsesLive -v ./internal/agent
func TestResponsesLive(t *testing.T) {
	if os.Getenv("AGENTGO_LIVE_RESPONSES") != "1" {
		t.Skip("set AGENTGO_LIVE_RESPONSES=1 to call the real endpoint")
	}
	key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT")
	if key == "" || ep == "" {
		t.Skip("AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT not set")
	}
	dep := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
	if dep == "" {
		dep = "gpt-6-luna"
	}
	t.Setenv("AGENTGO_REASONING_EFFORT", "high")
	m := newResponsesModel(LLMSettings{APIBase: strings.TrimRight(ep, "/") + "/openai/v1/", APIKey: key}, dep, 60*time.Second, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage(
		"A farmer has 17 sheep, all but 9 die, then buys twice as many as remain. How many sheep now? Think carefully.")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var reasoning, text strings.Builder
	var chunks int
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks++
		reasoning.WriteString(c.ReasoningContent)
		text.WriteString(c.Content)
		if c.ResponseMeta != nil && c.ResponseMeta.Usage != nil {
			t.Logf("usage: %+v", *c.ResponseMeta.Usage)
		}
	}
	t.Logf("chunks=%d\nREASONING: %q\nTEXT: %q", chunks, reasoning.String(), text.String())
	if !strings.Contains(text.String(), "27") {
		t.Errorf("answer lacks 27: %q", text.String())
	}
}
