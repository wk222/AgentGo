package codetools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"agentgo/third_party/crush/internal/history"
	"agentgo/third_party/crush/internal/pubsub"
)

// memHistory is an in-memory history.Service: the tools only need versioned
// file snapshots per session, not Crush's SQLite store.
type memHistory struct {
	*pubsub.Broker[history.File]
	mu    sync.Mutex
	seq   int64
	files map[string]history.File
}

func newMemHistory() *memHistory {
	return &memHistory{Broker: pubsub.NewBroker[history.File](), files: map[string]history.File{}}
}

func (h *memHistory) add(sessionID, path, content string, version int64) history.File {
	h.seq++
	now := time.Now().Unix()
	f := history.File{
		ID: fmt.Sprintf("h%d", h.seq), SessionID: sessionID, Path: path, Content: content,
		Version: version, CreatedAt: now, UpdatedAt: now,
	}
	h.files[f.ID] = f
	return f
}

func (h *memHistory) Create(_ context.Context, sessionID, path, content string) (history.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.add(sessionID, path, content, history.InitialVersion), nil
}

func (h *memHistory) CreateVersion(_ context.Context, sessionID, path, content string) (history.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := int64(history.InitialVersion)
	for _, f := range h.files {
		if f.SessionID == sessionID && f.Path == path && f.Version >= next {
			next = f.Version + 1
		}
	}
	return h.add(sessionID, path, content, next), nil
}

func (h *memHistory) Get(_ context.Context, id string) (history.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[id]
	if !ok {
		return history.File{}, errors.New("history: not found")
	}
	return f, nil
}

func (h *memHistory) GetByPathAndSession(_ context.Context, path, sessionID string) (history.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var best history.File
	found := false
	for _, f := range h.files {
		if f.SessionID == sessionID && f.Path == path && (!found || f.Version > best.Version) {
			best, found = f, true
		}
	}
	if !found {
		return history.File{}, errors.New("history: not found")
	}
	return best, nil
}

func (h *memHistory) ListBySession(_ context.Context, sessionID string) ([]history.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []history.File
	for _, f := range h.files {
		if f.SessionID == sessionID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (h *memHistory) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]history.File, error) {
	all, _ := h.ListBySession(ctx, sessionID)
	latest := map[string]history.File{}
	for _, f := range all {
		if cur, ok := latest[f.Path]; !ok || f.Version > cur.Version {
			latest[f.Path] = f
		}
	}
	out := make([]history.File, 0, len(latest))
	for _, f := range latest {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (h *memHistory) Delete(_ context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, id)
	return nil
}

func (h *memHistory) DeleteSessionFiles(_ context.Context, sessionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, f := range h.files {
		if f.SessionID == sessionID {
			delete(h.files, id)
		}
	}
	return nil
}

// memTracker is an in-memory filetracker.Service.
type memTracker struct {
	mu    sync.Mutex
	reads map[string]map[string]time.Time // session -> path -> last read
}

func newMemTracker() *memTracker { return &memTracker{reads: map[string]map[string]time.Time{}} }

func (t *memTracker) RecordRead(_ context.Context, sessionID, path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reads[sessionID] == nil {
		t.reads[sessionID] = map[string]time.Time{}
	}
	t.reads[sessionID][path] = time.Now()
}

func (t *memTracker) LastReadTime(_ context.Context, sessionID, path string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads[sessionID][path]
}

func (t *memTracker) ListReadFiles(_ context.Context, sessionID string) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.reads[sessionID]))
	for p := range t.reads[sessionID] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
