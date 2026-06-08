package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// EpisodicCompressor runs MemoryDistill when the session spine needs compaction.
type EpisodicCompressor struct {
	pipeline *Pipeline
	caller   LLMCaller
	mu       sync.Mutex
	lastRun  map[string]time.Time
	cooldown time.Duration
}

func NewEpisodicCompressor(pipeline *Pipeline, caller LLMCaller) *EpisodicCompressor {
	return &EpisodicCompressor{
		pipeline: pipeline,
		caller:   caller,
		lastRun:  make(map[string]time.Time),
		cooldown: 2 * time.Minute,
	}
}

// EpisodicCompressEnabled reports env / auto trigger policy.
func EpisodicCompressEnabled(autoTrigger bool) bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_EPISODIC_COMPRESS"))
	if v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	if v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return autoTrigger
}

// CompressIfNeeded returns an episodic summary line for the spine (empty if skipped).
func (c *EpisodicCompressor) CompressIfNeeded(ctx context.Context, scope string, messages []*schema.Message, autoTrigger bool) string {
	if c == nil || c.pipeline == nil || !EpisodicCompressEnabled(autoTrigger) {
		return ""
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "session"
	}
	c.mu.Lock()
	if prev, ok := c.lastRun[scope]; ok && time.Since(prev) < c.cooldown {
		c.mu.Unlock()
		return ""
	}
	c.lastRun[scope] = time.Now()
	c.mu.Unlock()

	conv := transcriptForCompress(messages, 24)
	if conv == "" {
		return ""
	}

	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var summary string
	var err error
	if c.caller != nil {
		if _, err = c.pipeline.RunJournal(runCtx, c.caller, scope, conv); err != nil {
			return ""
		}
		journals, _ := c.pipeline.store.ListByModality(runCtx, modalityJournal, scope, 5)
		var jb strings.Builder
		for _, j := range journals {
			jb.WriteString(j.Content)
			jb.WriteString("\n---\n")
		}
		snap, _ := c.pipeline.ContextPrompt(runCtx, scope)
		summary, err = c.pipeline.RunDistillLLM(runCtx, c.caller, scope, jb.String(), snap)
	} else {
		summary, err = c.pipeline.Distill(runCtx, scope, 30)
	}
	if err != nil || strings.TrimSpace(summary) == "" {
		return ""
	}
	return fmt.Sprintf("[episodic compress %s] %s", time.Now().Format("2006-01-02 15:04"), strings.TrimSpace(summary))
}

func transcriptForCompress(msgs []*schema.Message, maxLines int) string {
	var lines []string
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		c := strings.TrimSpace(msg.Content)
		if c == "" {
			continue
		}
		switch msg.Role {
		case schema.User, schema.Assistant:
			lines = append(lines, string(msg.Role)+": "+c)
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
