package memory

import (
	"agentgo/internal/promptbudget"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const atomicDistillSystem = `Extract at most 12 atomic memories from the supplied journal sources.
Each item expresses ONE durable constraint, preference, decision, fact, lesson or unresolved issue.
Treat existing memories as reference only. Preserve exact paths/identifiers and later corrections.
Never promote an assistant claim or summarized journal into verified success. Do not invent evidence.
Add a short stable topic label for directory navigation; omit it if unclear.
Return JSON only: {"atoms":[{"topic":"project or subject","kind":"constraint|preference|decision|fact|lesson|unresolved","content":"short statement","source_ids":["journal ID"]}]}.
Only cite supplied source IDs. Omit uncertain or unsupported items. Empty atoms is allowed.`

type distilledAtom struct {
	Kind    string   `json:"kind"`
	Topic   string   `json:"topic,omitempty"`
	Content string   `json:"content"`
	Sources []string `json:"source_ids"`
}

func (p *Pipeline) distillInput(ctx context.Context, scope, journalText, snapshot string) (string, map[string]bool, error) {
	journals, err := p.store.ListByModality(ctx, modalityJournal, scope, 7)
	if err != nil {
		return "", nil, err
	}
	known := map[string]bool{}
	var b strings.Builder
	b.WriteString("Existing memories (reference only):\n" + promptbudget.Excerpt(snapshot, 600) + "\nJournal sources:\n")
	for _, j := range journals {
		line := "[" + j.ID + "] " + promptbudget.Excerpt(j.Content, 500) + "\n"
		if promptbudget.Estimate(b.String()+line) > 3000 {
			continue
		}
		known[j.ID] = true
		b.WriteString(line)
	}
	if len(known) == 0 {
		b.WriteString("Unindexed input (no citable source ID):\n" + promptbudget.Excerpt(journalText, 1500))
	}
	return b.String(), known, nil
}

func (p *Pipeline) persistDistilledAtoms(ctx context.Context, parent Record, known map[string]bool) ([]Record, error) {
	var parsed struct {
		Atoms []distilledAtom `json:"atoms"`
	}
	if json.Unmarshal([]byte(cleanJSONResponse(parent.Content)), &parsed) != nil {
		return nil, nil
	} // Legacy text remains a reference summary.
	var accepted []Record
	seen := map[string]bool{}
	for _, atom := range parsed.Atoms {
		if len(accepted) >= 12 {
			break
		}
		switch atom.Kind {
		case "constraint", "preference", "decision", "fact", "lesson", "unresolved":
		default:
			continue
		}
		text := strings.TrimSpace(atom.Content)
		if len([]rune(text)) < 4 || len([]rune(text)) > 500 || len(atom.Sources) == 0 {
			continue
		}
		valid := true
		sources := []string{}
		for _, id := range atom.Sources {
			if !known[id] {
				valid = false
				break
			}
			sources = append(sources, id)
		}
		if !valid {
			continue
		}
		sum := sha256.Sum256([]byte(parent.Scope + "\x00" + atom.Kind + "\x00" + strings.ToLower(strings.Join(strings.Fields(text), " "))))
		id := fmt.Sprintf("atom_%x", sum[:12])
		if seen[id] {
			continue
		}
		seen[id] = true
		rec := Record{ID: id, Scope: parent.Scope, Content: text, Modality: "fact", Status: "active", Importance: 0.8, SourceTrust: 0.35,
			Metadata: map[string]interface{}{"memory_kind": atom.Kind, "verification": "derived_unverified", "source_ids": sources, "parent_id": parent.ID, "source": "llm_distillation"}}
		if topic := strings.TrimSpace(atom.Topic); topic != "" && len([]rune(topic)) <= 80 {
			rec.Metadata["topic"] = topic
		}
		// A repeated model summary must not reactivate an archived/disproved atom,
		// erase user feedback, or replace an existing atom's evidence lineage.
		if existing, err := p.store.GetRecord(ctx, id); err == nil {
			if existing.Status == "active" && existing.SupersedesID == "" && existing.ContradictedBy == "" {
				accepted = append(accepted, existing)
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return accepted, err
		}
		if err := p.Ingest(ctx, rec); err != nil {
			return accepted, err
		}
		accepted = append(accepted, rec)
	}
	return accepted, nil
}
