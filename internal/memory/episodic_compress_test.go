package memory

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestEpisodicCompressEnabledEnv(t *testing.T) {
	_ = os.Setenv("AGENTGO_EPISODIC_COMPRESS", "1")
	defer os.Unsetenv("AGENTGO_EPISODIC_COMPRESS")
	if !EpisodicCompressEnabled(false) {
		t.Fatal("expected enabled with env=1")
	}
}

func TestTranscriptForCompress(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
		schema.AssistantMessage("hi", nil),
	}
	if transcriptForCompress(msgs, 10) == "" {
		t.Fatal("expected transcript")
	}
}

func TestCompressSkipsWithoutPipeline(t *testing.T) {
	c := NewEpisodicCompressor(nil, nil)
	if c.CompressIfNeeded(context.Background(), "s", nil, true) != "" {
		t.Fatal("expected empty")
	}
}
