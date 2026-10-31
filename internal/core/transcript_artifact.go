package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"agentgo/internal/artifact"
)

// artifactScan is how many of a conversation's newest rows are searched for deliveries.
const artifactScan = 5000

// WithArtifactDir sets the folder deliveries are copied into (the one served for downloads). Without it
// a conversation cannot deliver files.
func (t *Transcript) WithArtifactDir(dir string) *Transcript {
	if t != nil {
		t.artifactDir = dir
	}
	return t
}

// ArtifactStore returns the delivery records of a conversation, kept as non-text rows next to the messages:
// they are left out of the model's history, and fork and delete treat them like any other message.
func (t *Transcript) ArtifactStore() artifact.Store {
	if t == nil || t.log == nil {
		return nil
	}
	return artifactRows{t.log}
}

// ArtifactDir is the folder deliveries are copied into.
func (t *Transcript) ArtifactDir() string {
	if t == nil {
		return ""
	}
	return t.artifactDir
}

type artifactRows struct{ log Log }

func (a artifactRows) Save(ctx context.Context, sessionID string, r artifact.Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.log.AppendMessage(pctx, sessionID, "assistant", string(raw), "artifact", nil)
}

func (a artifactRows) all(ctx context.Context, sessionID string) ([]artifact.Record, error) {
	msgs, err := a.log.GetMessages(ctx, sessionID, artifactScan)
	if err != nil {
		return nil, err
	}
	var out []artifact.Record
	for _, m := range msgs {
		if m.Type != "artifact" {
			continue
		}
		var r artifact.Record
		if json.Unmarshal([]byte(m.Content), &r) == nil && r.Path != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (a artifactRows) Latest(ctx context.Context, sessionID, path string) (artifact.Record, bool, error) {
	recs, err := a.all(ctx, sessionID)
	if err != nil {
		return artifact.Record{}, false, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Path == path {
			return recs[i], true, nil
		}
	}
	return artifact.Record{}, false, nil
}

// ListArtifacts returns the newest record of every delivered path, oldest first (removed ones included, marked).
func (t *Transcript) ListArtifacts(ctx context.Context, sessionID string) ([]artifact.Record, error) {
	sessionID = strings.TrimSpace(sessionID)
	if t == nil || t.log == nil || sessionID == "" {
		return nil, nil
	}
	recs, err := artifactRows{t.log}.all(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	latest := map[string]int{}
	for i, r := range recs {
		latest[r.Path] = i
	}
	var out []artifact.Record
	for i, r := range recs {
		if latest[r.Path] == i {
			out = append(out, r)
		}
	}
	return out, nil
}

// RemoveArtifact marks one delivery as removed by the user and deletes its downloadable copy. The same bytes are
// not attached again by a later delivery; changed bytes are a new version.
func (t *Transcript) RemoveArtifact(ctx context.Context, sessionID, artifactID string) (artifact.Record, bool, error) {
	recs, err := t.ListArtifacts(ctx, sessionID)
	if err != nil {
		return artifact.Record{}, false, err
	}
	for _, r := range recs {
		if r.ID != artifactID {
			continue
		}
		if r.Status == artifact.StatusRemoved {
			return r, true, nil
		}
		out, err := artifact.Tombstone(ctx, artifactRows{t.log}, t.artifactDir, sessionID, r)
		return out, true, err
	}
	return artifact.Record{}, false, nil
}
