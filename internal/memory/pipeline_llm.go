package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
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
	id := stableDerivedMemoryID("journal", scope, conversation)
	if p.store != nil {
		if existing, err := p.store.GetRecord(ctx, id); err == nil {
			if existing.Status == "active" && existing.SupersedesID == "" && existing.ContradictedBy == "" {
				return existing.Content, nil
			}
			return "", nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	text, err := caller(ctx, journalSystem, "请归纳以下对话:\n\n"+conversation)
	if err != nil || strings.TrimSpace(text) == "" || strings.TrimSpace(text) == "无" {
		return "", err
	}
	now := time.Now().Unix()
	rec := Record{
		ID: id, Content: strings.TrimSpace(text),
		Scope: scope, Modality: modalityJournal, Status: "active",
		Importance: 0.4, CreatedAt: now, UpdatedAt: now,
	}
	if err := p.Ingest(ctx, rec); err != nil {
		return "", err
	}
	return text, nil
}

// RunDistillLLM runs stage-2 DISTILL with LLM (replaces heuristic Distill when caller set).
func (p *Pipeline) RunDistillLLM(ctx context.Context, caller LLMCaller, scope string, journalText, memorySnapshot string) (string, error) {
	if caller == nil {
		return p.Distill(ctx, scope, 20)
	}
	user, known, err := p.distillInput(ctx, scope, journalText, memorySnapshot)
	if err != nil {
		return "", err
	}
	sourceRevision, err := p.store.ContextRevision(ctx, scope)
	if err != nil {
		return "", err
	}
	out, err := caller(ctx, atomicDistillSystem, user)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", err
	}
	currentRevision, err := p.store.ContextRevision(ctx, scope)
	if err != nil {
		return "", err
	}
	if currentRevision != sourceRevision {
		return "", ErrSourcesChanged
	}
	now := time.Now().Unix()
	rec := Record{ID: stableDerivedMemoryID("distill", scope, out), Content: out, Scope: scope, Modality: modalityReflection, Status: "active", Importance: 0.8, SourceTrust: 0.35, CreatedAt: now, UpdatedAt: now, Metadata: map[string]interface{}{"verification": "derived_unverified"}}
	if existing, lookupErr := p.store.GetRecord(ctx, rec.ID); lookupErr == nil {
		if existing.Status != "active" || existing.SupersedesID != "" || existing.ContradictedBy != "" {
			return "", nil
		}
		rec = existing
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return "", lookupErr
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(cleanJSONResponse(out)), &envelope) == nil && envelope["atoms"] != nil {
		rec.Metadata["atomic_container"] = true
	}
	if err := p.Ingest(ctx, rec); err != nil {
		return "", err
	}
	accepted, err := p.persistDistilledAtoms(ctx, rec, known)
	if err != nil {
		return "", err
	}
	if len(accepted) > 0 {
		rec.Metadata["atomic_children"] = true
		if err := p.Ingest(ctx, rec); err != nil {
			return "", err
		}
	}
	if err := p.store.SetPipelineState(ctx, "distill", now); err != nil {
		return "", err
	}
	if rec.Metadata["atomic_container"] == true {
		return packContextRecords(accepted, 500), nil
	}
	return out, nil
}

// RunReflect runs stage-3 REFLECT.
// Reflections stay in their originating scope; derived suggestions are not global user preferences.
func (p *Pipeline) RunReflect(ctx context.Context, caller LLMCaller, scope, memoryContent string) ([]string, error) {
	if caller == nil || memoryContent == "" {
		return nil, nil
	}
	out, err := caller(ctx, reflectSystem, "## MEMORY\n\n"+memoryContent)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	now := time.Now().Unix()
	parentID := fmt.Sprintf("reflect_%d", time.Now().UnixNano())
	if err := p.Ingest(ctx, Record{ID: parentID, Content: out, Scope: scope, Modality: modalityReflection, Status: "active", Importance: 0.8, SourceTrust: 0.35, CreatedAt: now, UpdatedAt: now, Metadata: map[string]interface{}{"verification": "derived_unverified"}}); err != nil {
		return nil, err
	}
	var insights []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			insights = append(insights, line)
			if err := p.Ingest(ctx, Record{ID: fmt.Sprintf("%s_insight_%d", parentID, len(insights)), Content: line, Scope: scope, Modality: modalityInsight, Status: "active", Importance: 0.8, SourceTrust: 0.35, CreatedAt: now, UpdatedAt: now, Metadata: map[string]interface{}{"verification": "derived_unverified", "parent_id": parentID}}); err != nil {
				return nil, err
			}
		}
	}
	if err := p.store.SetPipelineState(ctx, "reflect", now); err != nil {
		return nil, err
	}
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
	journals, err := p.store.ListByModality(ctx, modalityJournal, scope, 7)
	if err != nil {
		return err
	}
	var jb strings.Builder
	for _, j := range journals {
		jb.WriteString(j.Content)
		jb.WriteString("\n---\n")
	}
	snap, err := p.ContextPrompt(ctx, scope)
	if err != nil {
		return err
	}
	if _, err := p.RunDistillLLM(ctx, caller, scope, jb.String(), snap); err != nil {
		return err
	}
	if snap != "" {
		if _, err := p.RunReflect(ctx, caller, scope, snap); err != nil {
			return err
		}
	}
	_, err = p.RunGC(ctx, 30, 0.4)
	return err
}

func stableDerivedMemoryID(kind, scope, content string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + strings.TrimSpace(content)))
	return fmt.Sprintf("%s_%x", kind, sum[:16])
}
