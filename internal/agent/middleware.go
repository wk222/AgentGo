package agent

import (
	"agentgo/internal/promptbudget"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/config"
	"agentgo/internal/memory"
	"agentgo/internal/sessions"
)

// MemoryMiddleware is an Eino ADK Middleware that injects PyBot-style
// semantic memory context before the model call, and saves the transcript
// after the agent completes.
type MemoryMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	engine         memory.Engine
	cacheMu        sync.Mutex
	recallCache    map[string]memoryRecallEntry
	recallRevision uint64
	journalCaller  memory.LLMCaller // optional; used when AGENTGO_AUTO_JOURNAL=1
}

func NewMemoryMiddleware(engine memory.Engine) *MemoryMiddleware {
	return &MemoryMiddleware{engine: engine, recallCache: map[string]memoryRecallEntry{}}
}

// SetJournalCaller enables post-turn JOURNAL stage (PyBot MemoryDistill).
func (m *MemoryMiddleware) SetJournalCaller(caller memory.LLMCaller) {
	if m != nil {
		m.journalCaller = caller
		if p := memory.PipelineFromEngine(m.engine); p != nil {
			p.SummaryForest().SetCaller(caller)
			p.SetLearningCaller(caller)
		}
	}
}

// BeforeModelRewriteState is called before each model invocation.
func (m *MemoryMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	EmitTrace(ctx, "start", "Memory", "recall", "召回相关记忆")
	defer EmitTrace(ctx, "end", "Memory", "recall", "")
	if m == nil || m.engine == nil || state == nil {
		return ctx, state, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, state, err
	}
	if reader, ok := m.engine.(interface {
		ContextRevision(context.Context, string) (string, error)
	}); ok {
		scope := SessionIDFromContext(ctx)
		read := func(readCtx context.Context) (string, error) { return reader.ContextRevision(readCtx, scope) }
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		revision, err := read(rctx)
		cancel()
		if err != nil {
			revision = "unavailable:" + scope
			read = nil
		}
		ctx = sessions.WithMemoryRevision(ctx, revision, read)
		cleaned, changed := sessions.InvalidateMemorySummaries(state.Messages, revision)
		if changed {
			if cleaned == nil {
				return ctx, state, errors.New("invalid persisted projection memory")
			}
			if err := recordProjectionRewrite(ctx, state.Messages, cleaned); err != nil {
				return ctx, state, err
			}
			state.Messages = cleaned
		}
	}
	budget := memoryBudgetFrom(ctx)
	if budget == nil {
		budget = newMemoryReadBudget()
		ctx = context.WithValue(ctx, memoryReadKey{}, budget)
	}
	if cleaned, changed := pruneOlderMemoryReads(state.Messages, budget.id+"|"+sessions.MemoryRevision(ctx)); changed {
		if err := recordProjectionRewrite(ctx, state.Messages, cleaned); err != nil {
			return ctx, state, err
		}
		state.Messages = cleaned
	}
	budget.beginModelProjection()
	header := "[Injected Memory Context]\n"
	if promptbudget.Estimate(header) > budget.total {
		header = ""
	}
	ctx = memory.WithPromptBudget(ctx, max(0, budget.automaticAllowance()-promptbudget.Estimate(header)))
	promptCtx := m.cachedRecallForQuery(ctx, SessionIDFromContext(ctx), currentMemoryQuery(state.Messages))
	budget.chargeAutomatic(header + promptCtx)
	if folded, changed := foldMemoryReadMessages(state.Messages, max(0, budget.total-budget.automatic)); changed {
		if err := recordProjectionRewrite(ctx, state.Messages, folded); err != nil {
			return ctx, state, err
		}
		state.Messages = folded
	}
	if err := ctx.Err(); err != nil {
		return ctx, state, err
	}
	out := make([]*schema.Message, 0, len(state.Messages)+1)
	for _, msg := range state.Messages {
		if msg != nil && msg.Extra != nil && msg.Extra["agentgo.memory_context"] == true {
			continue
		}
		out = append(out, msg)
	}
	{
		msg := schema.SystemMessage(header + promptCtx)
		msg.Extra = map[string]any{"agentgo.memory_context": true, sessions.ExtraMemoryRevision: sessions.MemoryRevision(ctx), "_eino_adk_runtime_generated_system_message": true}
		out = append([]*schema.Message{msg}, out...)
	}
	state.Messages = out
	return ctx, state, nil
}

// AfterAgent is called after the agent run reaches a successful terminal state.
func (m *MemoryMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	if m == nil || m.engine == nil {
		return ctx, nil
	}
	if state != nil && len(state.Messages) > 0 {
		lastMsg := state.Messages[len(state.Messages)-1]
		if lastMsg != nil && lastMsg.Role == schema.Assistant && strings.TrimSpace(lastMsg.Content) != "" {
			scope := SessionIDFromContext(ctx)
			sm := SessionModeFromContext(ctx)
			extra := map[string]interface{}{}
			if sm.Profile != "" {
				extra["mode_profile"] = string(sm.Profile)
			}
			if record, ok := assistantEpisodeRecord(scope, lastMsg.Content, "adk_after_agent", extra); ok {
				if err := m.engine.Ingest(ctx, record); err == nil {
					m.invalidateRecall(scope)
				}
			}
			if config.AutoLearn.On() {
				for i := len(state.Messages) - 1; i >= 0; i-- {
					msg := state.Messages[i]
					if msg != nil && msg.Role == schema.User && !sessions.IsContextMessage(msg) {
						if p := memory.PipelineFromEngine(m.engine); p != nil {
							if _, err := p.QueueLearning(ctx, scope, "user_turn", msg.Content, "", ""); err != nil {
								log.Printf("[memory] queue learning: %v", err)
							}
						}
						break
					}
				}
			}
			m.maybeRunJournal(ctx, scope, state.Messages)
			m.maybeRunReflect(ctx, scope)
			m.maybeSyncGarden(ctx, scope)
			m.maybeRunGC(ctx)
		}
	}
	return ctx, nil
}

func (m *MemoryMiddleware) maybeRunReflect(ctx context.Context, scope string) {
	if m == nil || m.journalCaller == nil || !config.AutoReflect.On() {
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
		if _, err := p.RunReflect(rctx, m.journalCaller, scope, snap); err == nil {
			m.invalidateRecall(scope)
		}
	}()
}

func (m *MemoryMiddleware) maybeRunGC(ctx context.Context) {
	if !config.AutoGC.On() {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	go func() {
		gctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := p.RunGC(gctx, 30, 0.35); err == nil {
			m.invalidateAllRecall()
		}
	}()
}

func (m *MemoryMiddleware) maybeSyncGarden(ctx context.Context, scope string) {
	if !config.MemoryGarden.On() {
		return
	}
	p := memory.PipelineFromEngine(m.engine)
	if p == nil {
		return
	}
	// garden sync is best-effort; workspace root from env
	root := config.Workspace.Get()
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
	if m == nil || m.journalCaller == nil || !config.AutoJournal.On() {
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
		if _, err := p.RunJournal(jctx, m.journalCaller, scope, conv); err == nil {
			m.invalidateRecall(scope)
		}
	}()
}

func transcriptFromMessages(msgs []*schema.Message, maxTurns int) string {
	// Bound by messages, not physical lines: multiline code and user constraints
	// must retain their role and message boundary in distillation input.
	var selected []*schema.Message
	for _, msg := range msgs {
		if msg != nil && !sessions.IsContextMessage(msg) && strings.TrimSpace(msg.Content) != "" && (msg.Role == schema.User || msg.Role == schema.Assistant) {
			selected = append(selected, msg)
		}
	}
	if maxTurns > 0 && len(selected) > maxTurns*2 {
		selected = selected[len(selected)-maxTurns*2:]
	}
	// Bound background distillation independently of the foreground model window.
	const maxJournalRunes = 16000
	const maxMessageRunes = 4000
	var parts []string
	remaining := maxJournalRunes
	for i := len(selected) - 1; i >= 0 && remaining > 128; i-- {
		msg := selected[i]
		prefix := string(msg.Role) + ": "
		text := journalExcerpt(msg.Content, min(maxMessageRunes, remaining-len(prefix)-1))
		part := prefix + text + "\n"
		remaining -= len([]rune(part))
		parts = append(parts, part)
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
	}
	return strings.TrimSpace(b.String())
}

func journalExcerpt(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	marker := "\n[Journal excerpt: middle omitted; not a complete record]\n"
	available := limit - len([]rune(marker))
	if available <= 0 {
		return string(runes[:max(0, limit)])
	}
	head := available * 2 / 3
	return string(runes[:head]) + marker + string(runes[len(runes)-(available-head):])
}

type memoryRecallEntry struct {
	prompt     string
	query      string
	dependency string
	expires    time.Time
}

func (m *MemoryMiddleware) cachedRecall(ctx context.Context, scope string) string {
	return m.cachedRecallForQuery(ctx, scope, "")
}

func (m *MemoryMiddleware) cachedRecallForQuery(ctx context.Context, scope, query string) string {
	if memory.PromptTokenBudget(ctx) <= 0 {
		return ""
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	dependency := ""
	var readRevision func(context.Context) (string, error)
	if reader, ok := m.engine.(interface {
		ContextRevision(context.Context, string) (string, error)
	}); ok {
		readRevision = func(ctx context.Context) (string, error) { return reader.ContextRevision(ctx, scope) }
		var err error
		dependency, err = readRevision(runCtx)
		if err != nil {
			return ""
		}
	}
	m.cacheMu.Lock()
	previous, ok := m.recallCache[scope]
	cacheKey := fmt.Sprintf("%s|budget=%d|hierarchy=%t", query, memory.PromptTokenBudget(ctx), config.MemoryHierarchy.On())
	revision := m.recallRevision
	m.cacheMu.Unlock()
	if ok && previous.query == cacheKey && previous.dependency == dependency && time.Now().Before(previous.expires) {
		return previous.prompt
	}
	runCtx = memory.WithRecallQuery(runCtx, query)
	prompt, err := m.engine.ContextPrompt(runCtx, scope)
	if ctx.Err() != nil {
		return ""
	}
	if readRevision != nil {
		current, readErr := readRevision(runCtx)
		if readErr != nil || current != dependency {
			return ""
		}
	}
	ttl := 15 * time.Second
	if err != nil {
		prompt = ""
		ttl = 2 * time.Second
	} // Never resurrect expired/retracted facts on a failed refresh.
	prompt = promptbudget.Excerpt(prompt, memory.PromptTokenBudget(ctx))
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if revision != m.recallRevision {
		return ""
	}
	if m.recallCache == nil {
		m.recallCache = map[string]memoryRecallEntry{}
	}
	if len(m.recallCache) >= 128 {
		for key := range m.recallCache {
			if key != scope {
				delete(m.recallCache, key)
				break
			}
		}
	}
	m.recallCache[scope] = memoryRecallEntry{prompt: prompt, query: cacheKey, dependency: dependency, expires: time.Now().Add(ttl)}
	return prompt
}

func (m *MemoryMiddleware) invalidateRecall(scope string) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.recallRevision++
	delete(m.recallCache, scope)
}
func (m *MemoryMiddleware) invalidateAllRecall() {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.recallRevision++
	m.recallCache = map[string]memoryRecallEntry{}
}

func currentMemoryQuery(messages []*schema.Message) string {
	var selected []string
	for i := len(messages) - 1; i >= 0 && len(selected) < 3; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != schema.User || sessions.IsContextMessage(msg) {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > 500 {
			text = string(runes[:250]) + " … " + string(runes[len(runes)-250:])
		}
		selected = append(selected, text)
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return strings.Join(selected, "\n")
}
