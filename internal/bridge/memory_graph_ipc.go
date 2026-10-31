package bridge

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentgo/internal/memory"
)

// GetMemoryGraphView returns the memory graph for the console: mode "overview"
// (the most linked memories) or "ego" (the neighbourhood of center). modalities
// is a comma-separated list of kinds, empty for all. It only reads.
func (s *AppService) GetMemoryGraphView(scope, mode, center string, depth int, modalities string, limit int) map[string]any {
	if s.rt.memStore == nil {
		return map[string]any{"success": false, "error": "记忆服务不可用"}
	}
	var kinds []string
	for _, k := range strings.Split(modalities, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds = append(kinds, k)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	g, err := s.rt.memStore.Graph(ctx, memory.GraphRequest{
		Scope: memoryTreeScope(scope), Mode: mode, Center: center, Depth: depth, Modalities: kinds, Limit: limit,
	})
	if err != nil {
		if errors.Is(err, memory.ErrGraphNotFound) {
			return map[string]any{"success": false, "error": "这条记忆不存在，或不在当前范围内", "not_found": true}
		}
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "graph": g}
}
