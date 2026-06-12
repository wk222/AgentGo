package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/memory"
)

// MemoryMiddleware is an Eino ADK Middleware that injects PyBot-style
// semantic memory context before the model call, and saves the transcript
// after the agent completes.
type MemoryMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	engine        memory.Engine
	journalCaller memory.LLMCaller // optional; used when AGENTGO_AUTO_JOURNAL=1
}

func NewMemoryMiddleware(engine memory.Engine) *MemoryMiddleware {
	return &MemoryMiddleware{engine: engine}
}

// SetJournalCaller enables post-turn JOURNAL stage (PyBot MemoryDistill).
func (m *MemoryMiddleware) SetJournalCaller(caller memory.LLMCaller) {
	if m != nil {
		m.journalCaller = caller
	}
}

// BeforeModelRewriteState is called before each model invocation.
func (m *MemoryMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	EmitTrace(ctx, "start", "Memory", "recall", "召回相关记忆")
	defer EmitTrace(ctx, "end", "Memory", "recall", "")
	// Recall memories and inject into System prompt (hybrid engine applies its own timeout).
	promptCtx, err := m.engine.ContextPrompt(ctx, SessionIDFromContext(ctx))
	if err == nil && promptCtx != "" {
		hasSystem := false
		for i, msg := range state.Messages {
			if msg.Role == schema.System {
				state.Messages[i].Content = fmt.Sprintf("%s\n\n[Injected Memory Context]:\n%s", msg.Content, promptCtx)
				hasSystem = true
				break
			}
		}

		if !hasSystem {
			// Prepend a system message
			sysMsg := &schema.Message{
				Role:    schema.System,
				Content: fmt.Sprintf("[Injected Memory Context]:\n%s", promptCtx),
			}
			state.Messages = append([]*schema.Message{sysMsg}, state.Messages...)
		}
	}
	return ctx, state, nil
}

// AfterAgent is called after the agent run reaches a successful terminal state.
func (m *MemoryMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	if len(state.Messages) > 0 {
		lastMsg := state.Messages[len(state.Messages)-1]
		if lastMsg.Role == schema.Assistant {
			scope := SessionIDFromContext(ctx)
			sm := SessionModeFromContext(ctx)
			meta := map[string]interface{}{
				"source": "adk_after_agent",
			}

			if sm.Profile != "" {
				meta["mode_profile"] = string(sm.Profile)
			}
			record := memory.Record{
				ID:       fmt.Sprintf("turn_%d", time.Now().UnixNano()),
				Content:  lastMsg.Content,
				Scope:    scope,
				Modality: "episode",
				Status:   "active",
				Metadata: meta,
			}
			_ = m.engine.Ingest(ctx, record)
			m.maybeRunJournal(ctx, scope, state.Messages)
			m.maybeRunReflect(ctx, scope)
			m.maybeSyncGarden(ctx, scope)
			m.maybeRunGC(ctx)
		}
	}
	return ctx, nil
}

func (m *MemoryMiddleware) maybeRunReflect(ctx context.Context, scope string) {
	if m == nil || m.journalCaller == nil || strings.TrimSpace(os.Getenv("AGENTGO_AUTO_REFLECT")) != "1" {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		snap, _ := p.ContextPrompt(rctx, scope)
		if snap == "" {
			return
		}
		_, _ = p.RunReflect(rctx, m.journalCaller, scope, snap)
	}()
}

func (m *MemoryMiddleware) maybeRunGC(ctx context.Context) {
	if strings.TrimSpace(os.Getenv("AGENTGO_AUTO_GC")) != "1" {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	go func() {
		gctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = p.RunGC(gctx, 30, 0.35)
	}()
}

func (m *MemoryMiddleware) maybeSyncGarden(ctx context.Context, scope string) {
	if strings.TrimSpace(os.Getenv("AGENTGO_MEMORY_GARDEN")) != "1" {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	// garden sync is best-effort; workspace root from env
	root := strings.TrimSpace(os.Getenv("AGENTGO_WORKSPACE"))
	if root == "" {
		return
	}
	g := memory.NewMarkdownGarden(root, p.Store())
	go func() {
		gctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = g.SyncScope(gctx, scope, 40)
	}()
}

func (m *MemoryMiddleware) maybeRunJournal(ctx context.Context, scope string, msgs []*schema.Message) {
	if m == nil || m.journalCaller == nil || strings.TrimSpace(os.Getenv("AGENTGO_AUTO_JOURNAL")) != "1" {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	conv := transcriptFromMessages(msgs, 12)
	if conv == "" {
		return
	}
	go func() {
		jctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_, _ = p.RunJournal(jctx, m.journalCaller, scope, conv)
	}()
}

func transcriptFromMessages(msgs []*schema.Message, maxTurns int) string {
	var b strings.Builder
	n := 0
	for _, msg := range msgs {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		switch msg.Role {
		case schema.User, schema.Assistant:
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(msg.Content)
			b.WriteString("\n")
			n++
		}
	}
	if maxTurns > 0 && n > maxTurns*2 {
		// keep tail only
		lines := strings.Split(strings.TrimSpace(b.String()), "\n")
		if len(lines) > maxTurns*2 {
			lines = lines[len(lines)-maxTurns*2:]
		}
		return strings.Join(lines, "\n")
	}
	return strings.TrimSpace(b.String())
}
