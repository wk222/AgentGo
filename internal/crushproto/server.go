package crushproto

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// LSPClientInfo holds information about an LSP client's state.
type LSPClientInfo struct {
	Name            string    `json:"name"`
	State           int       `json:"state"`
	Error           string    `json:"error,omitempty"`
	DiagnosticCount int       `json:"diagnostic_count"`
	ConnectedAt     time.Time `json:"connected_at"`
}

// LSPProvider supplies real LSP state and diagnostics to the Crush server.
type LSPProvider interface {
	LSPStates(workDir string) map[string]LSPClientInfo
	LSPDiagnostics(workDir, lspName string) any
	StartLSP(ctx context.Context, workDir, path string)
	StopAllLSPs(ctx context.Context, workDir string)
	SetLSPEventListener(workDir string, fn func(evType, name string, state int, diagCount int))
}

// ModelInfo represents the active model and its provider.
type ModelInfo struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

// ModelProvider provides dynamic model discovery, switching, and provider catalogs.
type ModelProvider interface {
	CurrentModel(workDir string) ModelInfo
	SetModel(ctx context.Context, workDir string, model, provider string) error
	AvailableProviders(workDir string) []any
}

// Options configures a Server.
type Options struct {
	Runner Runner // nil: a scripted demo Runner
	Logf   func(format string, args ...any)
	// SkipPermissions starts every workspace in "yolo" mode (no approval prompts).
	SkipPermissions bool
	// Store persists sessions per project path; nil keeps everything in memory.
	Store Store
	// LSPProvider provides real LSP states and diagnostics; nil uses empty stubs.
	LSPProvider LSPProvider
	// ModelProvider provides dynamic model discovery and switching; nil uses static fixtures.
	ModelProvider ModelProvider
}

// Server speaks Crush's /v1 protocol.
type Server struct {
	opts     Options
	runner   Runner
	model    string
	provider string

	base   context.Context
	cancel context.CancelFunc

	mu  sync.Mutex
	wss map[string]*workspace
}

// New builds a Server. Static responses come from the recorded fixtures.
func New(opts Options) (*Server, error) {
	if opts.Logf == nil {
		opts.Logf = func(f string, a ...any) { log.Printf("[crushproto] "+f, a...) }
	}
	agent, err := fixtureMap("agent")
	if err != nil {
		return nil, err
	}
	mc, _ := agent["model_cfg"].(map[string]any)
	model, _ := mc["model"].(string)
	provider, _ := mc["provider"].(string)

	if opts.ModelProvider != nil {
		if info := opts.ModelProvider.CurrentModel(""); info.Model != "" {
			model = info.Model
			if info.Provider != "" {
				provider = info.Provider
			}
		}
	}

	s := &Server{opts: opts, runner: opts.Runner, model: model, provider: provider, wss: map[string]*workspace{}}
	if s.runner == nil {
		s.runner = ScriptRunner{}
	}
	s.base, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// Close cancels in-flight runs.
func (s *Server) Close() { s.cancel() }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// statusWriter records the status for the request log and keeps SSE flushing working.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Handler returns the HTTP handler for the whole /v1 surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Anything the client asks for that we do not implement yet shows up here:
		// that log IS the to-do list for the probe.
		writeErr(w, http.StatusNotImplemented, fmt.Sprintf("crushproto: %s %s not implemented", r.Method, r.URL.Path))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		mux.ServeHTTP(sw, r)
		s.opts.Logf("%s %s -> %d", r.Method, r.URL.Path, sw.status)
	})
}

func (s *Server) workspace(w http.ResponseWriter, r *http.Request) *workspace {
	s.mu.Lock()
	ws := s.wss[r.PathValue("id")]
	s.mu.Unlock()
	if ws == nil {
		writeErr(w, http.StatusNotFound, "workspace not found")
	}
	return ws
}
