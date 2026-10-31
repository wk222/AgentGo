package memory

import (
	"context"
	"errors"
	"fmt"
)

// ContextRevision changes on new facts or changes/removal of existing evidence,
// but not on recall counters or insertion of ordinary conversation episodes.
func (s *SQLiteStore) ContextRevision(ctx context.Context, scope string) (string, error) {
	var local, global int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM memory_source_revisions WHERE scope=?),0),COALESCE((SELECT revision FROM memory_source_revisions WHERE scope='global'),0)`, scope).Scan(&local, &global)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d", scope, local, global), nil
}
func (p *Pipeline) ContextRevision(ctx context.Context, scope string) (string, error) {
	return p.store.ContextRevision(ctx, scope)
}
func (h *HybridEngine) ContextRevision(ctx context.Context, scope string) (string, error) {
	return h.sqlite.ContextRevision(ctx, scope)
}

var ErrSourcesChanged = errors.New("memory sources changed during distillation")
