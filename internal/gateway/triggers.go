package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// TriggerBackend is implemented by backends that support event triggers. It is optional:
// the routes are only registered when the Backend also satisfies this interface.
type TriggerBackend interface {
	ListTriggers(ctx context.Context) ([]byte, error)
	CreateTrigger(ctx context.Context, body []byte) ([]byte, error)
	GetTrigger(ctx context.Context, id string) ([]byte, error)
	CancelTrigger(ctx context.Context, id string) error
	FireTrigger(ctx context.Context, id string, payload map[string]any) error
	EmitSignal(ctx context.Context, runID, name, payload string) error
}

func (s *Server) registerTriggerRoutes(mux *http.ServeMux) {
	tb, ok := s.backend.(TriggerBackend)
	if !ok {
		return
	}
	mux.HandleFunc("/api/v1/triggers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b, err := tb.ListTriggers(r.Context())
			writeJSONBytes(w, b, err, http.StatusInternalServerError)
		case http.MethodPost:
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			b, err := tb.CreateTrigger(r.Context(), body)
			writeJSONBytes(w, b, err, http.StatusBadRequest)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// /api/v1/triggers/{id}            GET, DELETE
	// /api/v1/triggers/{id}/fire       POST (webhook; body is attached to the event)
	mux.HandleFunc("/api/v1/triggers/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/triggers/"), "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id := parts[0]
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			b, err := tb.GetTrigger(r.Context(), id)
			writeJSONBytes(w, b, err, http.StatusNotFound)
		case len(parts) == 1 && r.Method == http.MethodDelete:
			if err := tb.CancelTrigger(r.Context(), id); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case len(parts) == 2 && parts[1] == "fire" && r.Method == http.MethodPost:
			var payload map[string]any
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if len(body) > 0 {
				if err := json.Unmarshal(body, &payload); err != nil {
					payload = map[string]any{"raw": string(body)}
				}
			}
			if err := tb.FireTrigger(r.Context(), id, payload); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v1/signals", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			RunID   string `json:"run_id"`
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.RunID == "" || in.Name == "" {
			http.Error(w, "run_id and name are required", http.StatusBadRequest)
			return
		}
		if err := tb.EmitSignal(r.Context(), in.RunID, in.Name, in.Payload); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}

func writeJSONBytes(w http.ResponseWriter, b []byte, err error, errStatus int) {
	if err != nil {
		http.Error(w, err.Error(), errStatus)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
