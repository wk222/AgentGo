package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Contradiction represents a detected logical mismatch between a new fact and an existing one.
type Contradiction struct {
	ExistingID  string `json:"existing_id"`
	Explanation string `json:"explanation"`
}

// TruthResolution defines the decision made by the truth maintenance engine.
type TruthResolution struct {
	Resolution      string `json:"resolution"` // supersede_a_with_b, supersede_b_with_a, merge, keep_both
	Explanation     string `json:"explanation"`
	MergedContent   string `json:"merged_content,omitempty"`
	AppliedRecordID string `json:"applied_record_id,omitempty"`
}

var detectSystem = `You are an AI truth maintenance system.
Given a new memory fact and a list of existing memory records, identify if any existing records LOGICALLY CONTRADICT the new fact.
Ignore minor details; only report clear contradictions or outdated statements.
Output your findings in JSON format ONLY:
[{"existing_id": "<id>", "explanation": "<why it contradicts>"}]
If there are no contradictions, return an empty JSON array: []`

var resolveSystem = `You are an AI truth maintenance system.
Compare two contradicting memory facts (Fact A and Fact B) and resolve the contradiction.
Decide if one fact supersedes the other, if they should be merged, or if they should both be kept.
Options:
- supersede_a_with_b: Fact B is newer/more accurate; Fact A is now obsolete.
- supersede_b_with_a: Fact A is newer/more accurate; Fact B is obsolete.
- merge: Combine them into a single, cohesive fact (provide in "merged_content").
- keep_both: The contradiction is minor or situational; keep both.

Output your decision in JSON format ONLY:
{"resolution": "<option>", "explanation": "<reason>", "merged_content": "<if merged>"}
Do not include any other markdown formatting.`

// DetectContradictions compares a new fact against a slice of existing records using the LLM.
func DetectContradictions(ctx context.Context, newFact string, existing []Record, caller LLMCaller) ([]Contradiction, error) {
	if caller == nil || len(existing) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString("Existing facts:\n")
	for _, rec := range existing {
		sb.WriteString(fmt.Sprintf("- [%s]: %s\n", rec.ID, rec.Content))
	}
	sb.WriteString("\nNew fact:\n" + newFact)

	resp, err := caller(ctx, detectSystem, sb.String())
	if err != nil {
		return nil, err
	}

	// Clean JSON output if LLM wraps in markdown code blocks
	resp = cleanJSONResponse(resp)

	var contradictions []Contradiction
	if err := json.Unmarshal([]byte(resp), &contradictions); err != nil {
		return nil, fmt.Errorf("failed to parse contradictions JSON: %w", err)
	}

	return contradictions, nil
}

// ResolveTruth compares two contradicting records and returns the resolution strategy.
func ResolveTruth(ctx context.Context, factA, factB Record, caller LLMCaller) (TruthResolution, error) {
	var res TruthResolution
	if caller == nil {
		res.Resolution = "keep_both"
		return res, nil
	}

	userMsg := fmt.Sprintf("Fact A:\nID: %s\nContent: %s\n\nFact B:\nID: %s\nContent: %s\n",
		factA.ID, factA.Content, factB.ID, factB.Content)

	resp, err := caller(ctx, resolveSystem, userMsg)
	if err != nil {
		return res, err
	}

	resp = cleanJSONResponse(resp)
	if err := json.Unmarshal([]byte(resp), &res); err != nil {
		return res, fmt.Errorf("failed to parse truth resolution JSON: %w", err)
	}

	return res, nil
}

func cleanJSONResponse(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// ApplyTruthResolution commits a resolved memory conflict to the store.
func ApplyTruthResolution(ctx context.Context, store *SQLiteStore, factA, factB Record, res TruthResolution) (Record, error) {
	if store == nil {
		return Record{}, fmt.Errorf("truth resolution: memory store unavailable")
	}
	if strings.TrimSpace(factA.ID) == "" || strings.TrimSpace(factB.ID) == "" {
		return Record{}, fmt.Errorf("truth resolution: both facts require ids")
	}
	switch strings.TrimSpace(res.Resolution) {
	case "supersede_a_with_b":
		if err := store.MarkSupersedes(ctx, factA.ID, factB.ID); err != nil {
			return Record{}, err
		}
		_ = store.MarkCanonical(ctx, factB.ID, true)
		_ = store.MarkContradiction(ctx, factA.ID, factB.ID)
		_ = store.Link(ctx, factB.ID, factA.ID, "supersedes")
		got, err := store.GetRecord(ctx, factB.ID)
		if err != nil {
			return factB, nil
		}
		res.AppliedRecordID = got.ID
		return got, nil
	case "supersede_b_with_a":
		if err := store.MarkSupersedes(ctx, factB.ID, factA.ID); err != nil {
			return Record{}, err
		}
		_ = store.MarkCanonical(ctx, factA.ID, true)
		_ = store.MarkContradiction(ctx, factB.ID, factA.ID)
		_ = store.Link(ctx, factA.ID, factB.ID, "supersedes")
		got, err := store.GetRecord(ctx, factA.ID)
		if err != nil {
			return factA, nil
		}
		res.AppliedRecordID = got.ID
		return got, nil
	case "merge":
		merged, err := mergedTruthRecord(factA, factB, res)
		if err != nil {
			return Record{}, err
		}
		if err := store.Ingest(ctx, merged); err != nil {
			return Record{}, err
		}
		_ = store.MarkSupersedes(ctx, factA.ID, merged.ID)
		_ = store.MarkSupersedes(ctx, factB.ID, merged.ID)
		_ = store.Link(ctx, merged.ID, factA.ID, "merged_from")
		_ = store.Link(ctx, merged.ID, factB.ID, "merged_from")
		got, err := store.GetRecord(ctx, merged.ID)
		if err != nil {
			return merged, nil
		}
		return got, nil
	case "keep_both", "":
		_ = store.Link(ctx, factA.ID, factB.ID, "truth_checked_keep_both")
		got, err := store.GetRecord(ctx, factB.ID)
		if err != nil {
			return factB, nil
		}
		return got, nil
	default:
		return Record{}, fmt.Errorf("truth resolution: unknown resolution %q", res.Resolution)
	}
}

func mergedTruthRecord(factA, factB Record, res TruthResolution) (Record, error) {
	content := strings.TrimSpace(res.MergedContent)
	if content == "" {
		content = strings.TrimSpace(factA.Content)
		if b := strings.TrimSpace(factB.Content); b != "" && b != content {
			content += "\n" + b
		}
	}
	if strings.TrimSpace(content) == "" {
		return Record{}, fmt.Errorf("truth resolution: merged content is empty")
	}
	scope := strings.TrimSpace(factB.Scope)
	if scope == "" {
		scope = strings.TrimSpace(factA.Scope)
	}
	modality := strings.TrimSpace(factB.Modality)
	if modality == "" {
		modality = strings.TrimSpace(factA.Modality)
	}
	if modality == "" {
		modality = "fact"
	}
	trust := factA.SourceTrust
	if factB.SourceTrust > trust {
		trust = factB.SourceTrust
	}
	if trust <= 0 {
		trust = 1.0
	}
	importance := factA.Importance
	if factB.Importance > importance {
		importance = factB.Importance
	}
	if importance <= 0 {
		importance = 1.0
	}
	now := time.Now().Unix()
	return Record{
		ID:          fmt.Sprintf("truth_merge_%d", time.Now().UnixNano()),
		Content:     content,
		Scope:       scope,
		Modality:    modality,
		Status:      "active",
		Importance:  importance,
		IsCanonical: true,
		SourceTrust: trust,
		Metadata: map[string]interface{}{
			"truth_resolution": res.Resolution,
			"source_a":         factA.ID,
			"source_b":         factB.ID,
			"explanation":      res.Explanation,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
