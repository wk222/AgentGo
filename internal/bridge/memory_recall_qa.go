package bridge

import "agentgo/internal/core"

// MemoryRecallQA returns recall rows plus the exact context prompt preview used
// to reason about injection quality from the desktop UI (logic lives in core).
func (s *AppService) MemoryRecallQA(query, scope, modality string, limit int) map[string]any {
	return core.RecallQA(s.rt.Memory(), query, scope, modality, limit)
}
