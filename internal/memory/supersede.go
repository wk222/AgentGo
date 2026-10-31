package memory

import (
	"context"
	"fmt"
	"time"
)

// SupersedeRecords atomically archives old facts, records their replacement,
// marks the replacement canonical and persists evidence links in one scope.
func (s *SQLiteStore) SupersedeRecords(ctx context.Context, scope, replacement string, obsolete ...string) error {
	return s.supersedeRecords(ctx, scope, replacement, nil, obsolete...)
}

func (s *SQLiteStore) mergeAndSupersede(ctx context.Context, merged Record, obsolete ...string) error {
	return s.supersedeRecords(ctx, merged.Scope, merged.ID, &merged, obsolete...)
}

func (s *SQLiteStore) supersedeRecords(ctx context.Context, scope, replacement string, merged *Record, obsolete ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if merged != nil {
		if err := ingestMemoryRecord(ctx, tx, *merged); err != nil {
			return err
		}
	}
	var replacementScope, status, next, contradicted string
	if err := tx.QueryRowContext(ctx, `SELECT scope,status,COALESCE(supersedes_id,''),COALESCE(contradicted_by,'') FROM memories WHERE id=?`, replacement).Scan(&replacementScope, &status, &next, &contradicted); err != nil {
		return err
	}
	if replacementScope != scope || status != "active" || next != "" || contradicted != "" {
		return fmt.Errorf("replacement must be an active fact in the same scope")
	}
	seen := map[string]bool{}
	for _, id := range obsolete {
		if id == replacement || id == "" {
			return fmt.Errorf("invalid self/empty replacement")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		var oldScope, oldStatus, oldNext string
		if err := tx.QueryRowContext(ctx, `SELECT scope,status,COALESCE(supersedes_id,'') FROM memories WHERE id=?`, id).Scan(&oldScope, &oldStatus, &oldNext); err != nil {
			return err
		}
		if oldScope != scope {
			return fmt.Errorf("cannot supersede a fact from another scope")
		}
		if oldStatus != "active" && !(oldStatus == "archived" && oldNext == replacement) {
			return fmt.Errorf("obsolete fact already has a different resolution")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memories SET status='archived',supersedes_id=?,contradicted_by=?,updated_at=? WHERE id=?`, replacement, replacement, time.Now().Unix(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_links(source_id,target_id,relation,weight,created_at) VALUES(?,?,'supersedes',1,?) ON CONFLICT(source_id,target_id) DO UPDATE SET relation='supersedes'`, replacement, id, time.Now().Unix()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memories SET is_canonical=1,updated_at=? WHERE id=?`, time.Now().Unix(), replacement); err != nil {
		return err
	}
	return tx.Commit()
}
