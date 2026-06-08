package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"agentgo/internal/db"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func (s *SQLiteStore) DB() *sql.DB { return s.db }

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Configure(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := initSchema(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &SQLiteStore{db: conn}, nil
}

func initSchema(db *sql.DB) error {
	base := `
	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		scope TEXT NOT NULL,
		modality TEXT NOT NULL,
		metadata TEXT,
		status TEXT DEFAULT 'active',
		importance REAL DEFAULT 1.0,
		last_recall_at INTEGER DEFAULT 0,
		created_at INTEGER,
		updated_at INTEGER
	);
	CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
		content, content='memories', content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
		INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
		INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
		INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
	END;
	`
	if _, err := db.Exec(base); err != nil {
		return err
	}
	// migrate older DBs
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN importance REAL DEFAULT 1.0`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN last_recall_at INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN recall_count INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN supersedes_id TEXT`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN is_canonical INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN source_trust REAL DEFAULT 1.0`)
	_, _ = db.Exec(`ALTER TABLE memories ADD COLUMN contradicted_by TEXT`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_pipeline_state (
			stage TEXT PRIMARY KEY,
			last_run_ts INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS memory_links (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			relation TEXT,
			weight REAL DEFAULT 1.0,
			created_at INTEGER,
			PRIMARY KEY(source_id, target_id)
		);
		CREATE INDEX IF NOT EXISTS idx_mem_links_target ON memory_links(target_id);
	`)
	return nil
}

func (s *SQLiteStore) Link(ctx context.Context, sourceID, targetID, relation string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_links (source_id, target_id, relation, weight, created_at)
		VALUES (?, ?, ?, 1.0, ?)
		ON CONFLICT(source_id, target_id) DO UPDATE SET weight = weight + 0.1, relation = excluded.relation
	`, sourceID, targetID, relation, time.Now().Unix())
	return err
}

func (s *SQLiteStore) SetPipelineState(ctx context.Context, stage string, ts int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_pipeline_state (stage, last_run_ts) VALUES (?, ?)
		ON CONFLICT(stage) DO UPDATE SET last_run_ts = excluded.last_run_ts
	`, stage, ts)
	return err
}

func (s *SQLiteStore) GetPipelineState(ctx context.Context, stage string) (int64, error) {
	row := s.db.QueryRowContext(ctx, `SELECT last_run_ts FROM memory_pipeline_state WHERE stage = ?`, stage)
	var ts int64
	if err := row.Scan(&ts); err != nil {
		return 0, err
	}
	return ts, nil
}

func (s *SQLiteStore) Ingest(ctx context.Context, record Record) error {
	if err := ApplyTaxonomy(&record); err != nil {
		return err
	}
	now := time.Now().Unix()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	if record.UpdatedAt == 0 {
		record.UpdatedAt = record.CreatedAt
	}
	if record.Importance <= 0 {
		record.Importance = 1.0
	}
	if record.SourceTrust <= 0 {
		record.SourceTrust = 1.0
	}
	metaJSON, _ := json.Marshal(record.Metadata)
	isCanonicalVal := 0
	if record.IsCanonical {
		isCanonicalVal = 1
	}
	query := `
		INSERT INTO memories (
			id, content, scope, modality, metadata, status, importance, last_recall_at,
			recall_count, supersedes_id, is_canonical, source_trust, contradicted_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			status = excluded.status,
			importance = excluded.importance,
			recall_count = excluded.recall_count,
			supersedes_id = excluded.supersedes_id,
			is_canonical = excluded.is_canonical,
			source_trust = excluded.source_trust,
			contradicted_by = excluded.contradicted_by,
			updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		record.ID, record.Content, record.Scope, record.Modality, string(metaJSON),
		record.Status, record.Importance, record.LastRecallAt, record.RecallCount,
		record.SupersedesID, isCanonicalVal, record.SourceTrust, record.ContradictedBy, record.CreatedAt, record.UpdatedAt)
	return err
}

func (s *SQLiteStore) scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var metaStr sql.NullString
	var imp sql.NullFloat64
	var lastRecall sql.NullInt64
	var recallCount int
	var supersedesID, contradictedBy sql.NullString
	var isCanonical int
	var sourceTrust sql.NullFloat64

	err := row.Scan(
		&r.ID, &r.Content, &r.Scope, &r.Modality, &metaStr, &r.Status,
		&imp, &lastRecall, &recallCount, &supersedesID, &isCanonical, &sourceTrust, &contradictedBy,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return r, err
	}
	if imp.Valid {
		r.Importance = imp.Float64
	} else {
		r.Importance = 1.0
	}
	if lastRecall.Valid {
		r.LastRecallAt = lastRecall.Int64
	}
	r.RecallCount = recallCount
	if supersedesID.Valid {
		r.SupersedesID = supersedesID.String
	}
	r.IsCanonical = isCanonical > 0
	if sourceTrust.Valid {
		r.SourceTrust = sourceTrust.Float64
	} else {
		r.SourceTrust = 1.0
	}
	if contradictedBy.Valid {
		r.ContradictedBy = contradictedBy.String
	}
	if metaStr.Valid && metaStr.String != "" {
		_ = json.Unmarshal([]byte(metaStr.String), &r.Metadata)
	}
	return r, nil
}

// IncrementRecallCount increments the hit count for a recalled memory.
func (s *SQLiteStore) IncrementRecallCount(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET recall_count = recall_count + 1, last_recall_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// MarkCanonical flags a memory entry as official/canonical.
func (s *SQLiteStore) MarkCanonical(ctx context.Context, id string, isCanonical bool) error {
	val := 0
	if isCanonical {
		val = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET is_canonical = ?, updated_at = ? WHERE id = ?`, val, time.Now().Unix(), id)
	return err
}

// MarkSupersedes marks id as obsolete and records supersedesID as its replacement.
func (s *SQLiteStore) MarkSupersedes(ctx context.Context, id string, supersedesID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET supersedes_id = ?, status = 'archived', updated_at = ? WHERE id = ?`, supersedesID, time.Now().Unix(), id)
	return err
}

// MarkContradiction links a memory entry that directly contradicts another.
func (s *SQLiteStore) MarkContradiction(ctx context.Context, id string, contradictedBy string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET contradicted_by = ?, updated_at = ? WHERE id = ?`, contradictedBy, time.Now().Unix(), id)
	return err
}

func (s *SQLiteStore) touchRecalledRecords(ctx context.Context, records []Record) {
	if len(records) == 0 {
		return
	}
	now := time.Now().Unix()
	seen := make(map[string]bool, len(records))
	for _, r := range records {
		if r.ID == "" || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		_, _ = s.db.ExecContext(ctx, `UPDATE memories SET recall_count = COALESCE(recall_count, 0) + 1, last_recall_at = ? WHERE id = ?`, now, r.ID)
	}
}

const selectCols = `m.id, m.content, m.scope, m.modality, m.metadata, m.status, m.importance, m.last_recall_at, m.recall_count, m.supersedes_id, m.is_canonical, m.source_trust, m.contradicted_by, m.created_at, m.updated_at`

func (s *SQLiteStore) Recall(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	rows, err := s.recallFTSMATCH(ctx, ftsMatchQuery(query), opts)
	if err == nil && len(rows) > 0 {
		return rows, nil
	}
	fb, fbErr := s.RecallLikeFallback(ctx, query, opts)
	if len(fb) > 0 {
		return fb, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fbErr
}

func (s *SQLiteStore) recallFTSMATCH(ctx context.Context, matchExpr string, opts RecallOptions) ([]Record, error) {
	sqlQuery := `
		SELECT ` + selectCols + `
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.status = 'active'
	`
	args := []interface{}{matchExpr}
	if opts.Scope != "" {
		sqlQuery += " AND m.scope = ?"
		args = append(args, opts.Scope)
	}
	if opts.StartTime > 0 {
		sqlQuery += " AND m.created_at >= ?"
		args = append(args, opts.StartTime)
	}
	if opts.Modality != "" {
		sqlQuery += " AND m.modality = ?"
		args = append(args, opts.Modality)
	}
	if opts.MinImportance > 0 {
		sqlQuery += " AND COALESCE(m.importance, 1.0) >= ?"
		args = append(args, opts.MinImportance)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	sqlQuery += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	var results []Record
	fetched := make(map[string]bool)
	for rows.Next() {
		r, err := s.scanRecord(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		results = append(results, r)
		fetched[r.ID] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// === Graph-Lite 1-Hop Expansion ===
	if len(results) > 0 {
		var ids []string
		for _, r := range results {
			ids = append(ids, r.ID)
		}

		placeholders := ""
		hopArgs := make([]interface{}, 0, len(ids)*2)
		for i, id := range ids {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			hopArgs = append(hopArgs, id)
		}
		for _, id := range ids {
			hopArgs = append(hopArgs, id)
		}

		hopQuery := `
			SELECT DISTINCT ` + selectCols + `
			FROM memories m
			JOIN memory_links l ON (m.id = l.target_id OR m.id = l.source_id)
			WHERE (l.source_id IN (` + placeholders + `) OR l.target_id IN (` + placeholders + `))
			  AND m.status = 'active'
		`

		hopRows, err := s.db.QueryContext(ctx, hopQuery, hopArgs...)
		if err == nil {
			for hopRows.Next() {
				r, err := s.scanRecord(hopRows)
				if err == nil && !fetched[r.ID] {
					results = append(results, r)
					fetched[r.ID] = true
				}
			}
			_ = hopRows.Close()
		}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	s.touchRecalledRecords(ctx, results)
	return results, nil
}

// RecallLikeFallback scans content with LIKE when FTS MATCH fails or times out (latency degrade path).
func (s *SQLiteStore) RecallLikeFallback(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	var clauses []string
	var args []interface{}
	for _, t := range tokens {
		if len(t) < 2 {
			continue
		}
		clauses = append(clauses, "m.content LIKE ?")
		args = append(args, "%"+t+"%")
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	sqlQuery := `SELECT ` + selectCols + ` FROM memories m WHERE m.status = 'active' AND (` + strings.Join(clauses, " OR ") + `)`
	if opts.Scope != "" {
		sqlQuery += " AND m.scope = ?"
		args = append(args, opts.Scope)
	}
	if opts.StartTime > 0 {
		sqlQuery += " AND m.created_at >= ?"
		args = append(args, opts.StartTime)
	}
	if opts.Modality != "" {
		sqlQuery += " AND m.modality = ?"
		args = append(args, opts.Modality)
	}
	if opts.MinImportance > 0 {
		sqlQuery += " AND COALESCE(m.importance, 1.0) >= ?"
		args = append(args, opts.MinImportance)
	}
	sqlQuery += " ORDER BY m.importance DESC, m.updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Record
	for rows.Next() {
		r, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	s.touchRecalledRecords(ctx, results)
	return results, nil
}

func (s *SQLiteStore) ApplyFeedback(ctx context.Context, id string, delta float64, kind FeedbackKind) error {
	if kind == FeedbackDisproved {
		_, err := s.db.ExecContext(ctx, `UPDATE memories SET status = 'forgotten', updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE memories SET importance = MAX(0.1, MIN(2.0, COALESCE(importance,1.0) + ?)), updated_at = ? WHERE id = ?
	`, delta, time.Now().Unix(), id)
	return err
}

func (s *SQLiteStore) Feedback(ctx context.Context, id string, kind FeedbackKind) error {
	return s.ApplyFeedback(ctx, id, feedbackDelta(kind), kind)
}

func (s *SQLiteStore) RunGC(ctx context.Context, ageDays, importanceFloor float64) (int, error) {
	cutoff := time.Now().Add(-time.Duration(ageDays*24) * time.Hour).Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE memories SET status = 'archived', updated_at = ?
		WHERE status = 'active' AND created_at < ? AND COALESCE(importance,1.0) < ?
	`, time.Now().Unix(), cutoff, importanceFloor)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) ListActive(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM memories m WHERE status = 'active' ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *SQLiteStore) ListByScope(ctx context.Context, scope string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT ` + selectCols + ` FROM memories m WHERE status = 'active'`
	args := []interface{}{}
	if strings.TrimSpace(scope) != "" {
		q += ` AND scope = ?`
		args = append(args, scope)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *SQLiteStore) ListByModality(ctx context.Context, modality, scope string, limit int) ([]Record, error) {
	q := `SELECT ` + selectCols + ` FROM memories m WHERE status = 'active' AND modality = ?`
	args := []interface{}{modality}
	if scope != "" {
		q += ` AND scope = ?`
		args = append(args, scope)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *SQLiteStore) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	return "", nil
}
