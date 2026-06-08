package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// LLMCaller is used for JOURNAL / DISTILL / REFLECT stages (PyBot memory/pipeline.py).
type LLMCaller func(ctx context.Context, system, user string) (string, error)

const (
	modalityJournal    = "journal"
	modalityReflection = "reflection"
	modalityInsight    = "insight"
)

var journalSystem = `你是对话记录助手。把以下对话归纳为当天的「分类事件日记」。
输出简洁条目，可选 [EPISODE] 块。无内容则输出「无」。`

var distillSystem = `你是记忆整理助手。根据日记与现有记忆，输出可写入长期记忆的要点列表（Markdown 列表）。`

var reflectSystem = `你是元认知助手。对长期记忆做简短反思，输出 [REFLECT] 段落与可执行的 insight 要点。`

// RunJournal runs stage-1 JOURNAL for a conversation transcript.
func (p *Pipeline) RunJournal(ctx context.Context, caller LLMCaller, scope string, conversation string) (string, error) {
	if caller == nil || strings.TrimSpace(conversation) == "" {
		return "", nil
	}
	text, err := caller(ctx, journalSystem, "请归纳以下对话:\n\n"+conversation)
	if err != nil || strings.TrimSpace(text) == "" || strings.Contains(text, "无") {
		return "", err
	}
	now := time.Now().Unix()
	rec := Record{
		ID: fmt.Sprintf("journal_%d", now), Content: strings.TrimSpace(text),
		Scope: scope, Modality: modalityJournal, Status: "active",
		Importance: 0.4, CreatedAt: now, UpdatedAt: now,
	}
	_ = p.Ingest(ctx, rec)
	return text, nil
}

// RunDistillLLM runs stage-2 DISTILL with LLM (replaces heuristic Distill when caller set).
func (p *Pipeline) RunDistillLLM(ctx context.Context, caller LLMCaller, scope string, journalText, memorySnapshot string) (string, error) {
	if caller == nil {
		return p.Distill(ctx, scope, 20)
	}
	user := fmt.Sprintf("## 现有记忆\n%s\n\n## 近期日记\n%s\n", memorySnapshot, journalText)
	out, err := caller(ctx, distillSystem, user)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", err
	}
	now := time.Now().Unix()
	rec := Record{
		ID: fmt.Sprintf("distill_%d", now), Content: out,
		Scope: scope, Modality: modalityReflection, Status: "active",
		Importance: 1.2, CreatedAt: now, UpdatedAt: now,
	}
	_ = p.Ingest(ctx, rec)
	_ = p.store.SetPipelineState(ctx, "distill", now)
	return out, nil
}

// RunReflect runs stage-3 REFLECT.
func (p *Pipeline) RunReflect(ctx context.Context, caller LLMCaller, scope, memoryContent string) ([]string, error) {
	if caller == nil || memoryContent == "" {
		return nil, nil
	}
	out, err := caller(ctx, reflectSystem, "## MEMORY\n\n"+memoryContent)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	now := time.Now().Unix()
	_ = p.Ingest(ctx, Record{
		ID: fmt.Sprintf("reflect_%d", now), Content: out,
		Scope: scope, Modality: modalityReflection, Status: "active",
		Importance: 1.0, CreatedAt: now, UpdatedAt: now,
	})
	var insights []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			insights = append(insights, line)
			_ = p.Ingest(ctx, Record{
				ID: fmt.Sprintf("insight_%d_%d", now, len(insights)),
				Content: line, Scope: "global", Modality: modalityInsight,
				Status: "active", Importance: 1.1, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	_ = p.store.SetPipelineState(ctx, "reflect", now)
	return insights, nil
}

// RunFullPipeline runs JOURNAL → DISTILL → REFLECT → GC (via Eino compose.Graph, falling back to procedural on compilation error).
func (p *Pipeline) RunFullPipeline(ctx context.Context, caller LLMCaller, scope, conversation string) error {
	runnable, err := p.BuildPipelineGraph(ctx, caller)
	if err != nil {
		return p.runFullPipelineProcedural(ctx, caller, scope, conversation)
	}
	outErr, err := runnable.Invoke(ctx, PipelineInput{Scope: scope, Conversation: conversation})
	if err != nil {
		return err
	}
	return outErr
}

func (p *Pipeline) runFullPipelineProcedural(ctx context.Context, caller LLMCaller, scope, conversation string) error {
	if _, err := p.RunJournal(ctx, caller, scope, conversation); err != nil {
		return err
	}
	journals, _ := p.store.ListByModality(ctx, modalityJournal, scope, 7)
	var jb strings.Builder
	for _, j := range journals {
		jb.WriteString(j.Content)
		jb.WriteString("\n---\n")
	}
	snap, _ := p.ContextPrompt(ctx, scope)
	if _, err := p.RunDistillLLM(ctx, caller, scope, jb.String(), snap); err != nil {
		return err
	}
	if snap != "" {
		_, _ = p.RunReflect(ctx, caller, scope, snap)
	}
	_, _ = p.RunGC(ctx, 30, 0.4)
	return nil
}
