package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/config"
	"agentgo/internal/sessions"
)

// EpisodicCompressor runs MemoryDistill when the session spine needs compaction.
type EpisodicCompressor struct {
	pipeline     *Pipeline
	caller       LLMCaller
	mu           sync.Mutex
	lastRun      map[string]time.Time
	cooldown     time.Duration
	inflight     map[string]bool
	summaries    map[string]string
	fingerprints map[string][32]byte
	workers      chan struct{}
}

func NewEpisodicCompressor(pipeline *Pipeline, caller LLMCaller) *EpisodicCompressor {
	return &EpisodicCompressor{
		pipeline: pipeline,
		caller:   caller,
		lastRun:  make(map[string]time.Time),
		cooldown: 2 * time.Minute,
		inflight: map[string]bool{}, summaries: map[string]string{}, fingerprints: map[string][32]byte{}, workers: make(chan struct{}, 2),
	}
}

// EpisodicCompressEnabled reports env / auto trigger policy.
func EpisodicCompressEnabled(autoTrigger bool) bool {
	if on, ok := config.EpisodicCompress.Lookup(); ok {
		return on
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

	return c.compressTranscript(ctx, scope, conv)
}

func (c *EpisodicCompressor) compressTranscript(ctx context.Context, scope, conv string) string {
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

// CachedSummary never blocks a model call on journal/distill work. A bounded
// worker pool refreshes changed sessions, and only successful summaries replace
// the last known result. Cancellation still follows the active run.
func (c *EpisodicCompressor) CachedSummary(ctx context.Context, scope string, msgs []*schema.Message, auto bool) string {
	if c == nil || c.pipeline == nil {
		return ""
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "session"
	}
	c.mu.Lock()
	previous := c.summaries[scope]
	if !EpisodicCompressEnabled(auto) || c.inflight[scope] || time.Since(c.lastRun[scope]) < c.cooldown {
		c.mu.Unlock()
		return previous
	}
	c.mu.Unlock()
	conv := transcriptForCompress(msgs, 24)
	fingerprint := sha256.Sum256([]byte(conv))
	c.mu.Lock()
	cached := c.summaries[scope]
	if !EpisodicCompressEnabled(auto) || conv == "" || c.inflight[scope] || c.fingerprints[scope] == fingerprint || time.Since(c.lastRun[scope]) < c.cooldown {
		c.mu.Unlock()
		return cached
	}
	select {
	case c.workers <- struct{}{}:
	default:
		c.mu.Unlock()
		return cached
	}
	if len(c.lastRun) >= 128 {
		for key := range c.lastRun {
			if !c.inflight[key] && key != scope {
				delete(c.lastRun, key)
				delete(c.summaries, key)
				delete(c.fingerprints, key)
				break
			}
		}
	}
	c.inflight[scope] = true
	c.lastRun[scope] = time.Now()
	c.mu.Unlock()
	go func() {
		defer func() { <-c.workers; c.mu.Lock(); delete(c.inflight, scope); c.mu.Unlock() }()
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		line := c.compressTranscript(runCtx, scope, conv)
		if line != "" {
			c.mu.Lock()
			c.summaries[scope] = line
			c.fingerprints[scope] = fingerprint
			c.mu.Unlock()
		}
	}()
	return cached
}

func transcriptForCompress(msgs []*schema.Message, maxLines int) string {
	var lines []string
	firstUser := ""
	for _, msg := range msgs {
		if msg == nil || sessions.IsContextMessage(msg) {
			continue
		}
		c := strings.TrimSpace(msg.Content)
		if c == "" {
			continue
		}
		switch msg.Role {
		case schema.User, schema.Assistant, schema.Tool:
			r := []rune(c)
			if len(r) > 4000 {
				c = string(r[:2600]) + " …[omitted]… " + string(r[len(r)-1400:])
			}
			line := string(msg.Role) + ": " + c
			if msg.Role == schema.User && firstUser == "" {
				firstUser = line
			}
			lines = append(lines, line)
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		if firstUser != "" && !strings.Contains(strings.Join(lines, "\n"), firstUser) {
			lines = append([]string{firstUser}, lines...)
		}
	}
	text := strings.Join(lines, "\n")
	r := []rune(text)
	if len(r) > 32000 {
		text = string(r[:16000]) + "\n[transcript excerpt shortened]\n" + string(r[len(r)-16000:])
	}
	return text
}
