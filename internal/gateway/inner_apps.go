package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (s *Server) handleInnerApps(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/inner-apps")
	path = strings.Trim(path, "/")
	if path == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		b, err := s.backend.ListInnerApps(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	parts := strings.Split(path, "/")
	name := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			b, err := s.backend.GetInnerApp(r.Context(), name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case http.MethodPost:
			s.handleInnerAppInvoke(w, r, name)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "invoke" && r.Method == http.MethodPost {
		s.handleInnerAppInvoke(w, r, name)
		return
	}
	if len(parts) >= 2 && parts[1] == "assets" {
		rel := strings.Join(parts[2:], "/")
		b, mime, err := s.backend.GetInnerAppAsset(r.Context(), name, rel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if mime != "" {
			w.Header().Set("Content-Type", mime)
		}
		_, _ = w.Write(b)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleInnerAppInvoke(w http.ResponseWriter, r *http.Request, name string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var in struct {
		Input       string `json:"input"`
		Capability  string `json:"capability"`
		Action      string `json:"action"`
		PayloadJSON string `json:"payload_json"`
	}
	_ = json.Unmarshal(body, &in)
	out, err := s.backend.InvokeInnerApp(r.Context(), name, in.Input, in.Capability, in.Action, in.PayloadJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
