package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config for the HTTP SSE gateway.
type Config struct {
	Addr   string
	Token  string // optional Bearer / X-AgentGo-Token
	Broker *Broker
}

// Server exposes REST + SSE endpoints (PyBot/coze external clients).
type Server struct {
	cfg     Config
	backend Backend
	http    *http.Server
}

func NewServer(cfg Config, backend Backend) *Server {
	if cfg.Broker == nil {
		cfg.Broker = NewBroker()
	}
	s := &Server{cfg: cfg, backend: backend}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/api/v1/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/v1/chat/cancel", s.handleChatCancel)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskRoutes)
	mux.HandleFunc("/api/v1/workflows", s.handleWorkflowList)
	mux.HandleFunc("/api/v1/workflows/", s.handleWorkflowItem)
	mux.HandleFunc("/api/v1/inner-apps", s.handleInnerApps)
	mux.HandleFunc("/api/v1/inner-apps/", s.handleInnerApps)
	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           corsMiddleware(authMiddleware(cfg.Token, mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) Broker() *Broker { return s.cfg.Broker }

func (s *Server) Start() error {
	log.Printf("[gateway] SSE listening on http://%s", s.cfg.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"service":"agentgo-gateway"}`))
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	swr, err := NewSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	emit := func(event string, data []byte) error {
		var payload any
		_ = json.Unmarshal(data, &payload)
		return swr.WriteEvent(event, payload)
	}
	res, err := s.backend.StreamChat(ctx, req, emit)
	if err != nil {
		_ = swr.WriteEvent("error", map[string]string{"error": err.Error()})
		return
	}
	_ = swr.WriteEvent("done", res)
}

func (s *Server) handleChatCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ok := s.backend.CancelSessionRun(r.Context(), in.SessionID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"canceled": ok})
}

func (s *Server) handleTaskRoutes(w http.ResponseWriter, r *http.Request) {
	// /api/v1/tasks/{id}/events?after_seq=0
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "events" {
		http.NotFound(w, r)
		return
	}
	taskID := parts[0]
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)

	swr, err := NewSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	emit := func(event string, data []byte) error {
		var payload any
		_ = json.Unmarshal(data, &payload)
		return swr.WriteEvent(event, payload)
	}
	if err := s.backend.ReplayTaskEvents(ctx, taskID, afterSeq, emit); err != nil {
		_ = swr.WriteEvent("error", map[string]string{"error": err.Error()})
		return
	}
	unsub := s.backend.RegisterTaskListener(taskID, func(event string, data []byte) {
		var payload any
		_ = json.Unmarshal(data, &payload)
		_ = swr.WriteEvent(event, payload) //nolint:errcheck
	})
	defer unsub()

	// keep connection until client disconnects
	<-ctx.Done()
}

func (s *Server) handleWorkflowList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := s.backend.ListWorkflows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) handleWorkflowItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	switch {
	case len(parts) == 2 && parts[1] == "flowgram":
		switch r.Method {
		case http.MethodGet:
			b, err := s.backend.GetWorkflowFlowgram(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case http.MethodPut:
			body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if err := s.backend.SaveWorkflowFlowgram(r.Context(), id, body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost:
		var in struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out, err := s.backend.RunWorkflow(r.Context(), id, in.Input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"output": out})
	default:
		http.NotFound(w, r)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-AgentGo-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.Header.Get("X-AgentGo-Token")
		}
		if got != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
