package memory

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Source states recorded on learned-document memories when the file they were
// learned from is no longer what it was.
const (
	SourceChanged = "changed"
	SourceDeleted = "deleted"
)

// FlagDocumentSource marks every active memory that was learned from the
// document `source` (the identifier learn_document was given, normally a
// workspace-relative path) as coming from a source that has since changed or
// been deleted, and returns how many it marked. It covers both the evidence
// kept for the document and the lessons distilled from it.
//
// It only annotates: nothing is archived, deleted or edited in content, so the
// user or the model decides whether to learn the document again or withdraw it.
// The annotation shows in recall results and in the prompt label, and because
// it changes the memory it also moves the revision the summary tree is built
// against, so summaries that include these memories are rebuilt. A record that
// already carries the state is left alone, so repeating the call changes
// nothing. Re-learning the document creates new records without the mark.
//
// The identifier is not tied to a workspace, so two workspaces that both learn
// a file with the same relative path share the mark; being only an annotation,
// the worst outcome is a note on a record that did not need it.
func (s *SQLiteStore) FlagDocumentSource(ctx context.Context, source, state string) (int, error) {
	source = strings.TrimSpace(source)
	if source == "" || len(source) > 1900 {
		return 0, errors.New("document source required")
	}
	if state != SourceChanged && state != SourceDeleted {
		return 0, errors.New("state must be changed or deleted")
	}
	prefix := source + "#section-"
	// A multi-section document is stored under source#section-N; match only a
	// numeric suffix so similarly named documents are not caught.
	ref := `(json_extract(metadata,'$.source_ref')=? OR
 (substr(json_extract(metadata,'$.source_ref'),1,length(?))=? AND length(json_extract(metadata,'$.source_ref'))>length(?)
 AND substr(json_extract(metadata,'$.source_ref'),length(?)+1) NOT GLOB '*[^0-9]*'))`
	res, err := s.db.ExecContext(ctx, `UPDATE memories SET metadata=json_set(COALESCE(NULLIF(metadata,''),'{}'),'$.source_state',?,'$.source_state_at',?)
 WHERE status='active' AND COALESCE(json_extract(metadata,'$.source_state'),'')<>?
 AND ((json_extract(metadata,'$.source')='personal_learning_evidence' AND json_extract(metadata,'$.source_kind')='document'
       AND (json_extract(metadata,'$.document_source')=? OR `+ref+`))
   OR (json_extract(metadata,'$.source')='personal_learning' AND `+ref+`))`,
		state, time.Now().Unix(), state,
		source, source, prefix, prefix, prefix, prefix,
		source, prefix, prefix, prefix, prefix)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// sourceStateNote is the label a memory carries once its source file has
// changed or gone; empty for a record whose source is as it was.
func sourceStateNote(r Record) string {
	switch r.Metadata["source_state"] {
	case SourceChanged:
		return "来源文件已变化，可能过时"
	case SourceDeleted:
		return "来源文件已删除"
	}
	return ""
}
