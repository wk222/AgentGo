package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/promptbudget"
	"github.com/google/uuid"
)

const documentLearningSchema = `CREATE TABLE IF NOT EXISTS memory_documents (
 scope TEXT NOT NULL, source TEXT NOT NULL, revision TEXT NOT NULL DEFAULT '',
 generation TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'deleted',
 updated_at INTEGER NOT NULL, PRIMARY KEY(scope,source));`

type DocumentLearningResult struct {
	SourceIDs []string `json:"source_ids,omitempty"`
	Revision  string   `json:"revision"`
	Changed   bool     `json:"changed"`
	Deleted   bool     `json:"deleted"`
}

// ReplaceDocumentLearning queues a whole supplied document atomically. Revision
// describes supplied text/topic, not a verified hash of a file on disk. Each
// activation gets new source IDs so an old worker cannot publish after A-B-A.
func (p *Pipeline) ReplaceDocumentLearning(ctx context.Context, scope, source, topic, text, expectedRevision string) (DocumentLearningResult, error) {
	if strings.TrimSpace(text) == "" || len([]rune(text)) > 64000 || len([]rune(topic)) > 80 {
		return DocumentLearningResult{}, errors.New("source and text required; document limit is 64000 characters and topic limit is 80")
	}
	runes := []rune(text)
	var sections []DocumentSection
	for _, r := range SplitDocumentText(text) {
		sections = append(sections, DocumentSection{Text: string(runes[r[0]:r[1]])})
	}
	hash := sha256.Sum256([]byte(topic + "\x00" + text))
	return p.changeDocumentLearning(ctx, scope, source, topic, sections, hex.EncodeToString(hash[:]), expectedRevision)
}

// DocumentSection is one stored piece of a learned document. Locator says where in the original it came from
// ("page 3", `sheet "Budget" rows 2-40`); Start and End are rune offsets in the parsed text. All are optional:
// a document supplied as plain text has none. The revision (the version of the document) comes from the text
// alone, so a better locator never makes a new version.
type DocumentSection struct {
	Text    string
	Locator string
	Heading string
	Start   int
	End     int
}

// ReplaceDocumentSections is ReplaceDocumentLearning for a document that was cut and located by the caller (the
// tool parses the file itself, so it knows each section's page or rows). The sections must be the document in
// order; the revision is the same hash ReplaceDocumentLearning gives the same text.
func (p *Pipeline) ReplaceDocumentSections(ctx context.Context, scope, source, topic string, sections []DocumentSection, expectedRevision string) (DocumentLearningResult, error) {
	var text strings.Builder
	for _, s := range sections {
		text.WriteString(s.Text)
	}
	if len(sections) == 0 || strings.TrimSpace(text.String()) == "" || len([]rune(text.String())) > 64000 || len([]rune(topic)) > 80 {
		return DocumentLearningResult{}, errors.New("source and text required; document limit is 64000 characters and topic limit is 80")
	}
	hash := sha256.Sum256([]byte(topic + "\x00" + text.String()))
	return p.changeDocumentLearning(ctx, scope, source, topic, sections, hex.EncodeToString(hash[:]), expectedRevision)
}

// RetractDocumentLearning withdraws only memories derived from this source in
// this origin scope. It neither deletes the source file nor edits independent
// user memories. Historical transcript snapshots remain addressable.
func (p *Pipeline) RetractDocumentLearning(ctx context.Context, scope, source, expectedRevision string) (DocumentLearningResult, error) {
	return p.changeDocumentLearning(ctx, scope, source, "", nil, "", expectedRevision)
}

// SplitDocumentText cuts text into the rune ranges learned sections are made of: each as long as fits the
// extraction budget, together tiling the whole text.
func SplitDocumentText(text string) [][2]int {
	runes := []rune(text)
	var sections [][2]int
	for start := 0; start < len(runes); {
		lo, hi := 1, min(16000, len(runes)-start)
		for lo < hi {
			n := (lo + hi + 1) / 2
			if promptbudget.Estimate(string(runes[start:start+n])) <= 3000 {
				lo = n
			} else {
				hi = n - 1
			}
		}
		sections = append(sections, [2]int{start, start + lo})
		start += lo
	}
	return sections
}

func (p *Pipeline) changeDocumentLearning(ctx context.Context, scope, source, topic string, sections []DocumentSection, revision, expected string) (DocumentLearningResult, error) {
	if p == nil || p.store == nil {
		return DocumentLearningResult{}, errors.New("learning store unavailable")
	}
	scope, source = strings.TrimSpace(scope), strings.TrimSpace(source)
	if scope == "" || source == "" || len(source) > 1900 {
		return DocumentLearningResult{}, errors.New("document scope and bounded source required")
	}
	tx, err := p.store.db.BeginTx(ctx, nil)
	if err != nil {
		return DocumentLearningResult{}, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	// Write first to serialize concurrent replacements before observing state.
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_documents(scope,source,updated_at) VALUES(?,?,?) ON CONFLICT(scope,source) DO NOTHING`, scope, source, now); err != nil {
		return DocumentLearningResult{}, err
	}
	var oldRevision, generation, state string
	if err = tx.QueryRowContext(ctx, `SELECT revision,generation,state FROM memory_documents WHERE scope=? AND source=?`, scope, source).Scan(&oldRevision, &generation, &state); err != nil {
		return DocumentLearningResult{}, err
	}
	if expected != "" && expected != oldRevision {
		return DocumentLearningResult{}, errors.New("source_changed: document revision differs from expected_revision")
	}
	result := DocumentLearningResult{Revision: revision, Deleted: len(sections) == 0}
	if state == "active" && revision == oldRevision && len(sections) > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT id,status FROM memories WHERE json_extract(metadata,'$.document_source')=? AND json_extract(metadata,'$.origin_scope')=? AND json_extract(metadata,'$.document_generation')=? ORDER BY CAST(json_extract(metadata,'$.document_section') AS INTEGER)`, source, scope, generation)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var id, status string
			if err = rows.Scan(&id, &status); err != nil {
				rows.Close()
				return result, err
			}
			if status != "active" {
				rows.Close()
				return result, errors.New("document evidence was withdrawn; supply a changed document revision")
			}
			result.SourceIDs = append(result.SourceIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
		if len(result.SourceIDs) != len(sections) {
			return result, errors.New("incomplete stored document revision")
		}
		return result, tx.Commit()
	}
	// Include legacy learn_document section IDs. Match only a numeric suffix,
	// not similarly named documents. Retraction triggers archive derived atoms
	// and cancel their jobs within this same transaction.
	prefix := source + "#section-"
	res, err := tx.ExecContext(ctx, `UPDATE memories SET status='archived',updated_at=? WHERE status='active'
 AND json_extract(metadata,'$.source')='personal_learning_evidence' AND json_extract(metadata,'$.source_kind')='document'
 AND json_extract(metadata,'$.origin_scope')=? AND (
 json_extract(metadata,'$.document_source')=? OR json_extract(metadata,'$.source_ref')=? OR
 (substr(json_extract(metadata,'$.source_ref'),1,length(?))=? AND length(json_extract(metadata,'$.source_ref'))>length(?)
 AND substr(json_extract(metadata,'$.source_ref'),length(?)+1) NOT GLOB '*[^0-9]*'))`, now, scope, source, source, prefix, prefix, prefix, prefix)
	if err != nil {
		return result, err
	}
	n, _ := res.RowsAffected()
	result.Changed = state == "active" || len(sections) > 0 || n > 0
	generation = uuid.NewString()
	for i, section := range sections {
		id := stableDerivedMemoryID("learnsrc", scope, "document\x00"+source+"\x00"+generation+fmt.Sprint(i))
		ref := source
		if len(sections) > 1 {
			ref = fmt.Sprintf("%s#section-%d", source, i+1)
		}
		record := Record{ID: id, Scope: "global", Modality: "journal", Content: section.Text, Status: "active", SourceTrust: 0.35, Importance: 1,
			Metadata: map[string]interface{}{"source": "personal_learning_evidence", "source_kind": "document", "origin_scope": scope, "source_ref": ref, "topic": topic, "atomic_container": true, "verification": "derived_unverified", "document_source": source, "document_revision": revision, "document_generation": generation, "document_section": i, "document_total": len(sections)}}
		// Where this piece sits in the original, so a citation can say "page 3" and a neighbour can be found.
		if section.Locator != "" {
			record.Metadata["document_locator"] = section.Locator
		}
		if section.Heading != "" {
			record.Metadata["document_heading"] = section.Heading
		}
		if section.End > section.Start {
			record.Metadata["document_start"], record.Metadata["document_end"] = section.Start, section.End
		}
		if err = ingestMemoryRecord(ctx, tx, record); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO memory_learning_jobs(source_id,origin_scope,kind,updated_at) VALUES(?,?,'document',?)`, id, scope, now); err != nil {
			return result, err
		}
		result.SourceIDs = append(result.SourceIDs, id)
	}
	state = "active"
	if result.Deleted {
		state = "deleted"
		result.Revision = oldRevision
		if result.Changed || oldRevision == "" {
			// Deletion must also change the CAS token: a replacement prepared
			// before withdrawal cannot silently reactivate a deleted source.
			tombstone := sha256.Sum256([]byte("document-deleted\x00" + oldRevision + "\x00" + generation))
			result.Revision = hex.EncodeToString(tombstone[:])
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_documents SET revision=?,generation=?,state=?,updated_at=? WHERE scope=? AND source=?`, result.Revision, generation, state, now, scope, source); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
