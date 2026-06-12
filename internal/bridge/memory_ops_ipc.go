package bridge

import (
	"context"
	"encoding/json"

	"agentgo/internal/memory"
)

func (s *AppService) UpdateMemory(recordJSON string) map[string]any {
	if s.rt.memStore == nil {
		return map[string]any{"success": false, "error": "memory store unavailable"}
	}
	var patch memory.Record
	if err := json.Unmarshal([]byte(recordJSON), &patch); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if patch.ID == "" {
		return map[string]any{"success": false, "error": "id required"}
	}
	ctx := context.Background()
	if err := s.rt.memStore.UpdateRecord(ctx, patch.ID, patch); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	r, _ := s.rt.memStore.GetRecord(ctx, patch.ID)
	return map[string]any{"success": true, "record": r}
}

func (s *AppService) LinkMemories(sourceID, targetID, relation string) map[string]any {
	ctx := context.Background()
	if err := s.rt.Memory().Link(ctx, sourceID, targetID, relation); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

func (s *AppService) GetMemoryGraph(centerID string) map[string]any {
	if s.rt.memStore == nil {
		return map[string]any{"success": false}
	}
	gv, err := s.rt.memStore.BuildGraphView(context.Background(), centerID, 32)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "graph": gv}
}

func (s *AppService) ListMemoriesByScope(scope string, limit int) []memory.Record {
	if s.rt.memStore == nil {
		return nil
	}
	rows, _ := s.rt.memStore.ListByScope(context.Background(), scope, limit)
	return rows
}

// DetectMemoryContradictions compares a new fact against existing records in a scope.
func (s *AppService) DetectMemoryContradictions(newFact, scope string) ([]memory.Contradiction, error) {
	if s.rt.memStore == nil || s.rt.agentRunner == nil {
		return nil, nil
	}
	ctx := context.Background()
	existing, _ := s.rt.memStore.ListByScope(ctx, scope, 50) // limit to recent 50 for context
	caller := func(ctx context.Context, system, user string) (string, error) {
		cfg := s.rt.LLMConfig()
		if cfg.APIKey == "" {
			return "", nil
		}
		return ChatOnce(ctx, cfg, system, user)
	}
	return memory.DetectContradictions(ctx, newFact, existing, caller)
}

// ResolveMemoryTruth resolves a contradiction between two memory records.
func (s *AppService) ResolveMemoryTruth(factAID, factBID string) (memory.TruthResolution, error) {
	var res memory.TruthResolution
	if s.rt.memStore == nil || s.rt.agentRunner == nil {
		return res, nil
	}
	ctx := context.Background()
	factA, err := s.rt.memStore.GetRecord(ctx, factAID)
	if err != nil {
		return res, err
	}
	factB, err := s.rt.memStore.GetRecord(ctx, factBID)
	if err != nil {
		return res, err
	}
	caller := func(ctx context.Context, system, user string) (string, error) {
		cfg := s.rt.LLMConfig()
		if cfg.APIKey == "" {
			return "", nil
		}
		return ChatOnce(ctx, cfg, system, user)
	}
	return memory.ResolveTruth(ctx, factA, factB, caller)
}

// ApplyMemoryTruthResolution commits a UI-confirmed truth resolution.
func (s *AppService) ApplyMemoryTruthResolution(factAID, factBID, resolutionJSON string) map[string]any {
	if s.rt.memStore == nil {
		return map[string]any{"success": false, "error": "memory store unavailable"}
	}
	ctx := context.Background()
	factA, err := s.rt.memStore.GetRecord(ctx, factAID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	factB, err := s.rt.memStore.GetRecord(ctx, factBID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	var res memory.TruthResolution
	if err := json.Unmarshal([]byte(resolutionJSON), &res); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	applied, err := memory.ApplyTruthResolution(ctx, s.rt.memStore, factA, factB, res)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "resolution": res}
	}
	res.AppliedRecordID = applied.ID
	return map[string]any{"success": true, "resolution": res, "record": applied}
}

// ResolveAndApplyMemoryTruth lets the memory console run the full TMS loop.
func (s *AppService) ResolveAndApplyMemoryTruth(factAID, factBID string) map[string]any {
	res, err := s.ResolveMemoryTruth(factAID, factBID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	b, _ := json.Marshal(res)
	return s.ApplyMemoryTruthResolution(factAID, factBID, string(b))
}
