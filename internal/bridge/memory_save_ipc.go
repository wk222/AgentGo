package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/memory"
)

// MemorySave stores an explicit memory record (used by Crush/MCP and the UI).
// scope defaults to "global", modality to "fact".
func (s *AppService) MemorySave(content, scope, modality string, importance float64) map[string]any {
	if s.rt.Memory() == nil {
		return map[string]any{"success": false, "error": "memory unavailable"}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return map[string]any{"success": false, "error": "content is required"}
	}
	if strings.TrimSpace(scope) == "" {
		scope = "global"
	}
	if strings.TrimSpace(modality) == "" {
		modality = "fact"
	}
	if importance <= 0 || importance > 1 {
		importance = 0.6
	}
	now := time.Now()
	rec := memory.Record{
		ID:          fmt.Sprintf("mcp_%d", now.UnixNano()),
		Content:     content,
		Scope:       scope,
		Modality:    modality,
		Status:      "active",
		Importance:  importance,
		SourceTrust: 0.8,
		Metadata:    map[string]interface{}{"source": "mcp"},
		CreatedAt:   now.Unix(),
		UpdatedAt:   now.Unix(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := s.rt.Memory().Ingest(ctx, rec); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "id": rec.ID}
}
