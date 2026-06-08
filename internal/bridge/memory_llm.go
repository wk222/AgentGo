package bridge

import (
	"context"

	"agentgo/internal/memory"
)

func (s *AppService) memoryLLMCaller() memory.LLMCaller {
	cfg := s.rt.LLMConfig()
	if cfg.APIKey == "" {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, error) {
		return ChatOnce(ctx, cfg, system, user)
	}
}

func (s *AppService) MemoryRunJournal(scope, conversation string) map[string]any {
	ctx := context.Background()
	p := s.rt.memoryPipeline()
	if p == nil {
		return map[string]any{"success": false, "error": "pipeline unavailable"}
	}
	if scope == "" {
		scope = "session"
	}
	text, err := p.RunJournal(ctx, s.memoryLLMCaller(), scope, conversation)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "journal": text}
}

func (s *AppService) MemoryRunFullPipeline(scope, conversation string) map[string]any {
	ctx := context.Background()
	p := s.rt.memoryPipeline()
	if p == nil {
		return map[string]any{"success": false}
	}
	if scope == "" {
		scope = "session"
	}
	if err := p.RunFullPipeline(ctx, s.memoryLLMCaller(), scope, conversation); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

func (s *AppService) MemoryDistillLLM(scope string) map[string]any {
	ctx := context.Background()
	p := s.rt.memoryPipeline()
	if p == nil {
		return map[string]any{"success": false}
	}
	if scope == "" {
		scope = "session"
	}
	journals, _ := s.rt.memStore.ListByModality(ctx, "journal", scope, 7)
	var conv string
	for _, j := range journals {
		conv += j.Content + "\n---\n"
	}
	snap, _ := s.rt.Memory().ContextPrompt(ctx, scope)
	summary, err := p.RunDistillLLM(ctx, s.memoryLLMCaller(), scope, conv, snap)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "summary": summary}
}
