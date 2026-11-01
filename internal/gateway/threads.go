package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ErrThreadNotFound is returned when an event names a thread nobody follows.
var ErrThreadNotFound = errors.New("no session follows this thread")

// ThreadBackend binds outside thread keys to conversations and delivers events
// to them. It is optional: the routes exist only when the Backend implements it.
type ThreadBackend interface {
	DeliverThread(ctx context.Context, key, source, text string, create bool) (sessionID string, err error)
	ListThreads(ctx context.Context) ([]byte, error)
	BindThread(ctx context.Context, key, sessionID, title string) error
	UnbindThread(ctx context.Context, key string) error
}

// Thread keys contain '/', '#' and ':' so they travel in the body or the query,
// never in the path.
//
//	POST   /api/v1/threads/events   {"key","text","source","create"}  deliver an event
//	GET    /api/v1/threads                                              list bindings
//	POST   /api/v1/threads          {"key","session_id","title"}       bind
//	DELETE /api/v1/threads?key=...                                      unbind
func (s *Server) registerThreadRoutes(mux *http.ServeMux) {
	tb, ok := s.backend.(ThreadBackend)
	if !ok {
		return
	}
	mux.HandleFunc("/api/v1/threads/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Key    string `json:"key"`
			Text   string `json:"text"`
			Source string `json:"source"`
			Create bool   `json:"create"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.Key == "" || in.Text == "" {
			http.Error(w, "key and text are required", http.StatusBadRequest)
			return
		}
		sid, err := tb.DeliverThread(r.Context(), in.Key, in.Source, in.Text, in.Create)
		if errors.Is(err, ErrThreadNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, _ := json.Marshal(map[string]string{"session_id": sid})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/v1/threads", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b, err := tb.ListThreads(r.Context())
			writeJSONBytes(w, b, err, http.StatusInternalServerError)
		case http.MethodPost:
			var in struct {
				Key       string `json:"key"`
				SessionID string `json:"session_id"`
				Title     string `json:"title"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.Key == "" || in.SessionID == "" {
				http.Error(w, "key and session_id are required", http.StatusBadRequest)
				return
			}
			if err := tb.BindThread(r.Context(), in.Key, in.SessionID, in.Title); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			key := r.URL.Query().Get("key")
			if key == "" {
				http.Error(w, "key is required", http.StatusBadRequest)
				return
			}
			if err := tb.UnbindThread(r.Context(), key); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
