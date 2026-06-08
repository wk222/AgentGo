package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MarkdownGarden exports active memories as workspace markdown files (PyBot Garden subset).
type MarkdownGarden struct {
	root  string
	store *SQLiteStore
}

func NewMarkdownGarden(workspaceRoot string, store *SQLiteStore) *MarkdownGarden {
	return &MarkdownGarden{
		root:  filepath.Join(workspaceRoot, "memory-garden"),
		store: store,
	}
}

// SyncScope writes up to limit records for scope into memory-garden/<scope>/.
func (g *MarkdownGarden) SyncScope(ctx context.Context, scope string, limit int) (int, error) {
	if g == nil || g.store == nil {
		return 0, fmt.Errorf("garden unavailable")
	}
	if limit <= 0 {
		limit = 50
	}
	records, err := g.store.ListByScope(ctx, scope, limit)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(g.root, sanitizePath(scope))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range records {
		if rec.Status != "" && rec.Status != "active" {
			continue
		}
		name := sanitizePath(rec.ID) + ".md"
		body := formatGardenMD(rec)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			continue
		}
		n++
	}
	return n, nil
}

func formatGardenMD(rec Record) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", rec.ID))
	b.WriteString(fmt.Sprintf("scope: %s\n", rec.Scope))
	b.WriteString(fmt.Sprintf("modality: %s\n", rec.Modality))
	if rec.Metadata != nil {
		if layer, ok := rec.Metadata["taxonomy_layer"].(string); ok {
			b.WriteString(fmt.Sprintf("taxonomy_layer: %s\n", layer))
		}
	}
	b.WriteString(fmt.Sprintf("importance: %.2f\n", rec.Importance))
	b.WriteString(fmt.Sprintf("updated: %s\n", time.Unix(rec.UpdatedAt, 0).Format(time.RFC3339)))
	b.WriteString("---\n\n")
	b.WriteString(rec.Content)
	if !strings.HasSuffix(rec.Content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func sanitizePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.ReplaceAll(s, string(os.PathSeparator), "_")
	s = strings.ReplaceAll(s, ":", "_")
	if s == "" {
		return "default"
	}
	return s
}
