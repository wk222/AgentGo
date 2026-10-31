package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"agentgo/internal/artifact"
	"agentgo/internal/evidence"
)

// RecordEvidence connects successful source reads to the existing transcript.
// Non-text rows are excluded from model history; fork/delete use the same
// transactional transcript operations as ordinary messages.
func (t *Transcript) RecordEvidence(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if t == nil || t.log == nil || sessionID == "" {
		return ctx
	}
	// Deliveries of files (the deliver_file tool) are recorded in the same transcript.
	ctx = artifact.WithStore(ctx, sessionID, artifactRows{t.log}, t.artifactDir)
	return evidence.WithRecorder(ctx, sessionID, func(_ context.Context, record evidence.Record) error {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return t.log.AppendMessage(pctx, sessionID, "assistant", string(raw), "source_evidence", nil)
	})
}
