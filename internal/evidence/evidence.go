// Package evidence defines source snapshots shared by tools and transcripts.
// It owns no storage or execution loop; the transcript supplies persistence.
package evidence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"unicode/utf8"
)

type Span struct {
	Type     string `json:"type"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Mapping  string `json:"mapping"`
	Page     int    `json:"page,omitempty"`
	Sheet    string `json:"sheet,omitempty"`
	RowStart int    `json:"row_start,omitempty"`
	RowEnd   int    `json:"row_end,omitempty"`
}

type Citation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Spans    []Span `json:"source_spans,omitempty"`
}

type Record struct {
	SnapshotID string   `json:"snapshot_id"`
	Citation   Citation `json:"citation"`
	Text       string   `json:"text"`
	Untrusted  bool     `json:"untrusted"`
}

type runKey struct{}
type recorderKey struct{}
type recorder struct {
	scope string
	save  func(context.Context, Record) error
}
type run struct {
	mu          sync.Mutex
	salt, scope string
	bytes       int
	records     map[string]Record
	streaks     map[string]int
}

// StreakCount reads the count Streak keeps, without changing it.
func StreakCount(ctx context.Context, scope, key string) int {
	r, _ := ctx.Value(runKey{}).(*run)
	if r == nil || r.scope != scope {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.streaks[key]
}

// Streak counts consecutive misses under key within one run: a miss adds one, a
// hit starts over. It returns the count after this call, or 0 outside a run, so
// a tool can tell a model that keeps rewording a search which finds nothing to
// stop and answer instead of burning its turns.
func Streak(ctx context.Context, scope, key string, miss bool) int {
	r, _ := ctx.Value(runKey{}).(*run)
	if r == nil || r.scope != scope {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streaks == nil {
		r.streaks = map[string]int{}
	}
	if !miss {
		delete(r.streaks, key)
		return 0
	}
	r.streaks[key]++
	return r.streaks[key]
}

func WithRecorder(ctx context.Context, scope string, save func(context.Context, Record) error) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder{scope, save})
}

// WithRun preserves the registry for nested calls in the same execution. A
// fresh caller context never acquires citation eligibility from stored history.
func WithRun(ctx context.Context, scope string) context.Context {
	if r, ok := ctx.Value(runKey{}).(*run); ok && r.scope == scope {
		return ctx
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return ctx
	}
	return context.WithValue(ctx, runKey{}, &run{salt: hex.EncodeToString(salt[:]), scope: scope, records: map[string]Record{}})
}

func digest(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }

func Register(ctx context.Context, scope string, c Citation, text string) *Citation {
	r, _ := ctx.Value(runKey{}).(*run)
	if r == nil || r.scope != scope || ctx.Err() != nil || text == "" || c.Revision == "" || c.Source == "" || c.Start < 0 || c.End-c.Start != utf8.RuneCountInString(text) {
		return nil
	}
	c.ID = ""
	c.Spans = append([]Span(nil), c.Spans...)
	record := Record{Citation: c, Text: text, Untrusted: true}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil
	}
	record.SnapshotID = "es_" + digest(raw)
	c.ID = "ev_" + digest([]byte(r.salt + record.SnapshotID))[:32]
	record.Citation = c
	raw, _ = json.Marshal(record)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.records[c.ID]; !exists {
		if len(r.records) >= 128 || r.bytes+len(raw) > 2*1024*1024 {
			return nil
		}
		if sink, ok := ctx.Value(recorderKey{}).(recorder); ok {
			if sink.scope != scope || sink.save == nil || sink.save(ctx, clone(record)) != nil {
				return nil
			}
		}
		r.records[c.ID] = record
		r.bytes += len(raw)
	}
	c.Spans = append([]Span(nil), c.Spans...)
	return &c
}

func Read(ctx context.Context, scope, id string) (Record, error) {
	r, _ := ctx.Value(runKey{}).(*run)
	if r != nil && r.scope == scope {
		r.mu.Lock()
		record, ok := r.records[id]
		r.mu.Unlock()
		if ok {
			return clone(record), nil
		}
	}
	return Record{}, fmt.Errorf("evidence_unavailable: ID was not read in this run; read the original source again")
}

func clone(record Record) Record {
	record.Citation.Spans = append([]Span(nil), record.Citation.Spans...)
	return record
}
