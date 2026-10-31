// Package memeval runs a retrieval-evaluation suite through the real memory engine.
//
// The corpus is ingested into a throw-away in-memory SQLite database by memory.HybridEngine and
// searched with HybridEngine.Recall, so the numbers describe the code that answers an agent's
// memory queries, chunker included. Nothing leaves the process: the engine is built with an empty
// BootConfig, so there is no embedding call, no Milvus and no LLM query rewrite. What is measured
// is therefore the full-text path, which is all a build without a knowledge base has and is the
// lexical half of a hybrid one. It says nothing about vector search.
//
// The suite format, the metrics and the comparison live in internal/evaluation and know nothing
// about memory; this package is the only place that does.
package memeval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agentgo/internal/evaluation"
	"agentgo/internal/memory"
)

// scope keeps the evaluation's records apart from anything else in the database. The database is
// private to a Backend, so this is only a label.
const scope = "retrieval-eval"

// Description says what answered the queries. Reports carry it so a number is never read as more
// than it is.
const Description = "memory.HybridEngine over SQLite FTS5 with LIKE fallback; no embeddings, no Milvus, no LLM query rewrite"

// Backend holds one indexed corpus.
type Backend struct {
	store *memory.SQLiteStore
	eng   *memory.HybridEngine
	// at stamps every record, so no document looks newer than another: the LIKE fallback picks its
	// candidates by recency, and a stamp taken at ingest time would make that depend on the clock.
	at int64
	// names maps the record ID of a document to the ID it has in the suite. The suite's own IDs are
	// paths like "finance/faq.txt" and may contain anything; the engine only ever sees "d00001".
	names map[string]string
}

// New opens an empty in-memory index.
func New(ctx context.Context) (*Backend, error) {
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	eng, err := memory.NewHybridEngine(ctx, store, memory.BootConfig{})
	if err != nil {
		_ = store.DB().Close()
		return nil, fmt.Errorf("open index: %w", err)
	}
	return &Backend{store: store, eng: eng, at: time.Now().Unix(), names: map[string]string{}}, nil
}

// Close drops the index.
func (b *Backend) Close() error { return b.store.DB().Close() }

// Index ingests the corpus through the engine's own ingest path, which cuts long documents into
// chunks with whatever chunker memory currently has. It may be called once.
func (b *Backend) Index(ctx context.Context, docs []evaluation.CorpusDoc) (evaluation.IndexStats, error) {
	if len(b.names) > 0 {
		return evaluation.IndexStats{}, errors.New("the index already holds a corpus")
	}
	for i, d := range docs {
		id := fmt.Sprintf("d%05d", i+1)
		b.names[id] = d.ID
		rec := memory.Record{
			ID:      id,
			Content: d.Text,
			Scope:   scope,
			// Ingest cuts every record longer than 1200 bytes except an episode (a remembered
			// conversation turn), so a corpus under test must not be one. A document is a
			// statement of fact as far as memory is concerned.
			Modality:    "fact",
			Status:      "active",
			Importance:  1,
			SourceTrust: 1,
			CreatedAt:   b.at,
			UpdatedAt:   b.at,
		}
		if err := b.eng.Ingest(ctx, rec); err != nil {
			return evaluation.IndexStats{}, fmt.Errorf("index %s: %w", d.ID, err)
		}
	}
	return b.stats(ctx, len(docs))
}

// stats measures what ended up in the database, so it reports what the chunker really did.
func (b *Backend) stats(ctx context.Context, docs int) (evaluation.IndexStats, error) {
	rows, err := b.store.DB().QueryContext(ctx, `SELECT content FROM memories WHERE scope = ?`, scope)
	if err != nil {
		return evaluation.IndexStats{}, err
	}
	defer rows.Close()
	st := evaluation.IndexStats{Docs: docs}
	var total int
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return evaluation.IndexStats{}, err
		}
		n := utf8.RuneCountInString(content)
		st.Chunks++
		total += n
		st.MaxChars = max(st.MaxChars, n)
	}
	if err := rows.Err(); err != nil {
		return evaluation.IndexStats{}, err
	}
	if st.Chunks > 0 {
		st.MeanChars = total / st.Chunks
	}
	return st, nil
}

// Search recalls the k best chunks for query and returns the suite ID of the document each one
// was cut from. A hit that belongs to no indexed document is an error rather than something to
// skip: it would mean the chunk naming that this package relies on has changed.
func (b *Backend) Search(ctx context.Context, query string, k int) ([]string, error) {
	recs, err := b.eng.Recall(ctx, query, memory.RecallOptions{Scope: scope, Limit: k})
	if err != nil {
		return nil, err
	}
	if len(recs) > k {
		recs = recs[:k]
	}
	hits := make([]string, 0, len(recs))
	for _, r := range recs {
		name, ok := b.names[documentOf(r.ID)]
		if !ok {
			return nil, fmt.Errorf("hit %q is not a chunk of any indexed document", r.ID)
		}
		hits = append(hits, name)
	}
	return hits, nil
}

// documentOf maps a hit's record ID to the record ID of its document. Ingest names the chunks of a
// long document "<id>_c<n>"; a short document keeps its own ID.
func documentOf(id string) string {
	i := strings.LastIndex(id, "_c")
	if i <= 0 || i+2 >= len(id) {
		return id
	}
	for _, c := range id[i+2:] {
		if c < '0' || c > '9' {
			return id
		}
	}
	return id[:i]
}

// Chunker returns the splitter called name, ready for memory.SetChunker: "bytes" is the built-in
// splitter, "runes" is the same splitter measuring characters instead of bytes.
//
// Ingest asks every chunker for pieces of at most 1200, whatever the unit. For Chinese that is
// about 400 characters when counted in bytes and 1200 when counted in runes, so switching the unit
// also triples the chunk size. size, when positive, replaces the 1200 so the two effects can be
// told apart: runes at 400 against bytes at 1200 cut Chinese into pieces of the same length. With
// size 0 and "bytes" the result is nil, which SetChunker takes as "use the default": that arm is
// then exactly what production runs.
func Chunker(name string, size int) (memory.ChunkFunc, error) {
	var split memory.ChunkFunc
	switch name {
	case "", "bytes":
		if size <= 0 {
			return nil, nil
		}
		split = memory.ChunkContent
	case "runes":
		split = memory.ChunkContentRunes
	default:
		return nil, fmt.Errorf("unknown chunker %q (want bytes or runes)", name)
	}
	if size <= 0 {
		return split, nil
	}
	return func(content string, _, overlap int) []string { return split(content, size, overlap) }, nil
}

// Run indexes the suite's corpus with chunker (nil means the built-in splitter) and runs its
// cases at k, and labels the report with label. The chunker is installed for the duration of the
// call and then put back. It is process-wide state, so Run must not be called concurrently.
func Run(ctx context.Context, s *evaluation.RetrievalSuite, chunker memory.ChunkFunc, label string, k int) (*evaluation.RetrievalReport, error) {
	restore := memory.SetChunker(chunker)
	defer restore()
	b, err := New(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	stats, err := b.Index(ctx, s.Docs)
	if err != nil {
		return nil, err
	}
	rep, err := evaluation.RunRetrieval(ctx, s.Name, s.Cases, k, b.Search)
	if err != nil {
		return nil, err
	}
	rep.Label, rep.Backend, rep.Index = label, Description, &stats
	return rep, nil
}
