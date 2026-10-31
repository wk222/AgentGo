package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/schema"
)

const ExtraMemoryRevision = "agentgo.memory_source_revision.v1"

var ErrSummarySourceChanged = errors.New("memory sources changed while summary was generated")

type memoryRevisionKey struct{}
type memoryRevisionSnapshot struct {
	revision string
	read     func(context.Context) (string, error)
}

func WithMemoryRevision(ctx context.Context, revision string, read func(context.Context) (string, error)) context.Context {
	return context.WithValue(ctx, memoryRevisionKey{}, memoryRevisionSnapshot{revision, read})
}
func MemoryRevision(ctx context.Context) string {
	v, _ := ctx.Value(memoryRevisionKey{}).(memoryRevisionSnapshot)
	return v.revision
}
func ValidateMemoryRevision(ctx context.Context) error {
	v, ok := ctx.Value(memoryRevisionKey{}).(memoryRevisionSnapshot)
	if !ok || v.read == nil {
		return nil
	}
	current, err := v.read(ctx)
	if err != nil {
		return err
	}
	if current != v.revision {
		return ErrSummarySourceChanged
	}
	return nil
}

func StampSummaryRevision(messages []*schema.Message, revision string) {
	if revision == "" {
		return
	}
	for _, m := range messages {
		if ContextKind(m) == "summary" {
			if m.Extra == nil {
				m.Extra = map[string]any{}
			}
			m.Extra[ExtraMemoryRevision] = revision
		}
	}
}

// Drop invalid derived views without mutating archived message objects.
// Source notes, goals and real user/tool messages remain intact.
func InvalidateMemorySummaries(messages []*schema.Message, revision string) ([]*schema.Message, bool) {
	if revision == "" {
		return messages, false
	}
	out := make([]*schema.Message, 0, len(messages))
	changed := false
	for _, m := range messages {
		if m == nil {
			out = append(out, m)
			continue
		}
		if ContextKind(m) == "summary" && m.Extra[ExtraMemoryRevision] != revision {
			changed = true
			continue
		}
		raw, ok := m.Extra[extraProjectionMemory].(string)
		if !ok {
			out = append(out, m)
			continue
		}
		var memory ProjectionMemory
		if json.Unmarshal([]byte(raw), &memory) != nil {
			// Preserve real messages and immutable archive records. A broken optional
			// projection must not make the conversation unable to recover its history.
			copy := *m
			copy.Extra = map[string]any{}
			for k, v := range m.Extra {
				if k != extraProjectionMemory {
					copy.Extra[k] = v
				}
			}
			if ContextKind(m) == "runtime" {
				copy.Content = "[Recovery metadata unavailable] The saved derived state could not be decoded. Retrieve original session history before relying on old task claims."
				copy.Extra[ExtraContextKind] = "hygiene"
			}
			out = append(out, &copy)
			changed = true
			continue
		}
		if memory.MemoryRevision == revision {
			out = append(out, m)
			continue
		}
		memory.Summaries = nil
		memory.RecalledMemory = nil
		memory.MemoryRevision = revision
		copy := *m
		copy.Extra = map[string]any{}
		for k, v := range m.Extra {
			copy.Extra[k] = v
		}
		encoded, _ := json.Marshal(memory)
		copy.Extra[extraProjectionMemory] = string(encoded)
		if ContextKind(m) == "runtime" {
			copy.Content = "[Runtime memory changed] Previous derived summary and recalled excerpts were invalidated; use current recall and user corrections."
		}
		out = append(out, &copy)
		changed = true
	}
	return out, changed
}
