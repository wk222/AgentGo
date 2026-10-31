// Package docindex is a small persistent full-text index over documents that
// were parsed from a workspace. It stores text chunks together with the rune
// range each chunk occupies in the parsed document, so a hit can be cited at
// the same granularity parse_file uses (page, or sheet and row).
//
// The package knows nothing about file formats or the workspace. Callers parse
// the file, cut it into chunks and hand them over; the store keeps the chunks,
// answers keyword queries and forgets a source on request. Deciding whether an
// indexed source is still current is the caller's job: the store records the
// file's size, modification time, hash and parser version so the caller can.
//
// Chinese is searched without a word-segmentation dictionary: each run of Han
// characters is indexed as overlapping two-character pieces, and a query is cut
// the same way, so "出差报销怎么走" finds "出差报销单必须在返程后提交".
package docindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// File is what the index remembers about one indexed source.
type File struct {
	Scope         string `json:"scope"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	ParserVersion string `json:"parser_version"`
	Revision      string `json:"revision"`
	Size          int64  `json:"size"`
	ModTime       int64  `json:"mtime"` // unix nanoseconds
	TotalChars    int    `json:"total_chars"`
	ChunkCount    int    `json:"chunks"`
	IndexedAt     int64  `json:"indexed_at"` // unix seconds
	Note          string `json:"note,omitempty"`
}

// Chunk is one searchable piece of a document. Start and End are half-open rune
// offsets into the complete parsed text; Text is exactly that range.
type Chunk struct {
	ID     string
	Ord    int
	Start  int
	End    int
	Header string          // heading path, searched together with Text
	Text   string          // the exact text of [Start, End)
	Spans  json.RawMessage // source locators overlapping the range, caller-defined JSON
}

// Hit is a chunk returned by Search.
type Hit struct {
	Chunk
	Source   string
	Revision string
	Score    float64 // lower is better (bm25)
}

// Store is safe for concurrent use. It opens its tables on first use, so it can
// be created before the database is.
type Store struct {
	dbFn func() *sql.DB

	mu    sync.Mutex // serialises writers and schema creation
	ready *sql.DB    // the handle the schema was created on
}

// New returns a store over the database dbFn yields; dbFn may return nil until
// the database is available.
func New(dbFn func() *sql.DB) *Store { return &Store{dbFn: dbFn} }

// ErrUnavailable means the database behind the store is not open.
var ErrUnavailable = errors.New("document index unavailable: database is not open")

const schema = `
CREATE TABLE IF NOT EXISTS doc_index_files (
	scope TEXT NOT NULL,
	source TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	parser_version TEXT NOT NULL,
	revision TEXT NOT NULL,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	total_chars INTEGER NOT NULL,
	chunk_count INTEGER NOT NULL,
	indexed_at INTEGER NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (scope, source)
);
CREATE TABLE IF NOT EXISTS doc_index_chunks (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL,
	scope TEXT NOT NULL,
	source TEXT NOT NULL,
	revision TEXT NOT NULL,
	ord INTEGER NOT NULL,
	start_pos INTEGER NOT NULL,
	end_pos INTEGER NOT NULL,
	header TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL,
	spans TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_doc_index_chunks_source ON doc_index_chunks(scope, source);
CREATE VIRTUAL TABLE IF NOT EXISTS doc_index_fts USING fts5(terms);
`

func (s *Store) open() (*sql.DB, error) {
	if s == nil || s.dbFn == nil {
		return nil, ErrUnavailable
	}
	db := s.dbFn()
	if db == nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready == db {
		return db, nil
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("document index schema: %w", err)
	}
	s.ready = db
	return db, nil
}

// Replace stores f and its chunks as the complete current content of the
// source, in one transaction: a reader sees the old chunks or the new ones,
// never a mixture, and a failure leaves the old content in place.
func (s *Store) Replace(ctx context.Context, f File, chunks []Chunk) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	if f.Scope == "" || f.Source == "" || f.Revision == "" {
		return fmt.Errorf("document index: scope, source and revision are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteSource(ctx, tx, f.Scope, f.Source); err != nil {
		return err
	}
	for _, c := range chunks {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO doc_index_chunks(id,scope,source,revision,ord,start_pos,end_pos,header,text,spans) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			c.ID, f.Scope, f.Source, f.Revision, c.Ord, c.Start, c.End, c.Header, c.Text, string(c.Spans))
		if err != nil {
			return err
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO doc_index_fts(rowid, terms) VALUES(?, ?)`, seq, Terms(c.Header+"\n"+c.Text)); err != nil {
			return err
		}
	}
	f.ChunkCount = len(chunks)
	if f.IndexedAt == 0 {
		f.IndexedAt = time.Now().Unix()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO doc_index_files(scope,source,sha256,parser_version,revision,size,mtime,total_chars,chunk_count,indexed_at,note) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		f.Scope, f.Source, f.SHA256, f.ParserVersion, f.Revision, f.Size, f.ModTime, f.TotalChars, f.ChunkCount, f.IndexedAt, f.Note); err != nil {
		return err
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deleteSource(ctx context.Context, tx execer, scope, source string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM doc_index_fts WHERE rowid IN (SELECT seq FROM doc_index_chunks WHERE scope=? AND source=?)`, scope, source); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_index_chunks WHERE scope=? AND source=?`, scope, source); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM doc_index_files WHERE scope=? AND source=?`, scope, source)
	return err
}

// Remove forgets one source and returns whether it was indexed.
func (s *Store) Remove(ctx context.Context, scope, source string) (bool, error) {
	db, err := s.open()
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM doc_index_files WHERE scope=? AND source=?`, scope, source).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if err := deleteSource(ctx, tx, scope, source); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Touch records a new size and modification time for a source whose content
// was verified to be unchanged, so the cheap stat comparison succeeds again.
func (s *Store) Touch(ctx context.Context, scope, source string, size, mtime int64) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = db.ExecContext(ctx, `UPDATE doc_index_files SET size=?, mtime=? WHERE scope=? AND source=?`, size, mtime, scope, source)
	return err
}

const fileColumns = `scope,source,sha256,parser_version,revision,size,mtime,total_chars,chunk_count,indexed_at,note`

func scanFile(row interface{ Scan(...any) error }) (File, error) {
	var f File
	err := row.Scan(&f.Scope, &f.Source, &f.SHA256, &f.ParserVersion, &f.Revision, &f.Size, &f.ModTime, &f.TotalChars, &f.ChunkCount, &f.IndexedAt, &f.Note)
	return f, err
}

// Get returns one indexed source.
func (s *Store) Get(ctx context.Context, scope, source string) (File, bool, error) {
	db, err := s.open()
	if err != nil {
		return File{}, false, err
	}
	f, err := scanFile(db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM doc_index_files WHERE scope=? AND source=?`, scope, source))
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, false, nil
	}
	return f, err == nil, err
}

// List returns the indexed sources of a scope, optionally only those equal to
// prefix or inside the directory prefix, ordered by path.
func (s *Store) List(ctx context.Context, scope, prefix string) ([]File, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + fileColumns + ` FROM doc_index_files WHERE scope=?`
	args := []any{scope}
	if clause, extra := prefixClause("source", prefix); clause != "" {
		q += " AND " + clause
		args = append(args, extra...)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY source`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// prefixClause matches a source equal to prefix or inside the directory prefix.
func prefixClause(col, prefix string) (string, []any) {
	prefix = strings.Trim(strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/"), "/")
	if prefix == "" || prefix == "." {
		return "", nil
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	return "(" + col + "=? OR " + col + ` LIKE ? ESCAPE '\')`, []any{prefix, esc + "/%"}
}

// SearchOptions narrows a query.
type SearchOptions struct {
	Prefix  string   // only this source or directory
	Exclude []string // sources whose chunks must not be returned
	Limit   int
}

// Search returns the best matching chunks of a scope, best first.
func (s *Store) Search(ctx context.Context, scope, query string, opt SearchOptions) ([]Hit, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	match := MatchQuery(query)
	if match == "" {
		return nil, nil
	}
	limit := opt.Limit
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	q := `SELECT c.id,c.source,c.revision,c.ord,c.start_pos,c.end_pos,c.header,c.text,c.spans,bm25(doc_index_fts)
FROM doc_index_fts JOIN doc_index_chunks c ON c.seq = doc_index_fts.rowid
WHERE doc_index_fts MATCH ? AND c.scope = ?`
	args := []any{match, scope}
	if clause, extra := prefixClause("c.source", opt.Prefix); clause != "" {
		q += " AND " + clause
		args = append(args, extra...)
	}
	if len(opt.Exclude) > 0 {
		q += " AND c.source NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(opt.Exclude)), ",") + ")"
		for _, e := range opt.Exclude {
			args = append(args, e)
		}
	}
	q += ` ORDER BY bm25(doc_index_fts), c.source, c.ord LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		var spans string
		if err := rows.Scan(&h.ID, &h.Source, &h.Revision, &h.Ord, &h.Start, &h.End, &h.Header, &h.Text, &spans, &h.Score); err != nil {
			return nil, err
		}
		h.Spans = json.RawMessage(spans)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Around returns the chunk of a source that starts at the character offset (or,\n// failing that, the first that contains it: chunkers that overlap their chunks\n// make several contain one offset, and a hit's own start identifies it),
// together with up to before chunks ahead of it and after chunks behind it, in
// document order. Only chunks of the given revision count: if the source has
// been re-indexed since the caller saw it, nothing is returned, so the
// neighbours always belong to the same version of the document as the chunk
// the caller started from. center is the position of that chunk in the result.
func (s *Store) Around(ctx context.Context, scope, source, revision string, offset, before, after int) (chunks []Hit, center int, err error) {
	db, err := s.open()
	if err != nil {
		return nil, 0, err
	}
	if before < 0 || after < 0 {
		return nil, 0, fmt.Errorf("document index: before and after must not be negative")
	}
	var ord int
	err = db.QueryRowContext(ctx,
		`SELECT ord FROM doc_index_chunks WHERE scope=? AND source=? AND revision=? AND start_pos<=? AND end_pos>?
ORDER BY CASE WHEN start_pos=? THEN 0 ELSE 1 END, ord LIMIT 1`,
		scope, source, revision, offset, offset, offset).Scan(&ord)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id,source,revision,ord,start_pos,end_pos,header,text,spans FROM doc_index_chunks
WHERE scope=? AND source=? AND revision=? AND ord BETWEEN ? AND ? ORDER BY ord`,
		scope, source, revision, ord-before, ord+after)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var h Hit
		var spans string
		if err := rows.Scan(&h.ID, &h.Source, &h.Revision, &h.Ord, &h.Start, &h.End, &h.Header, &h.Text, &spans); err != nil {
			return nil, 0, err
		}
		h.Spans = json.RawMessage(spans)
		if h.Ord == ord {
			center = len(chunks)
		}
		chunks = append(chunks, h)
	}
	return chunks, center, rows.Err()
}
