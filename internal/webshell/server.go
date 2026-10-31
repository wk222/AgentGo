// Package webshell serves AgentGo's UI to a normal browser. It replaces the
// Wails window: the same frontend loads /wails/runtime.js (an HTTP/SSE shim),
// and every Call.ByName becomes a request that is dispatched to the same
// service methods the desktop build exposes.
package webshell

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed runtime.js
var runtimeJS []byte

// CookieName carries the access token once the page was opened with ?t=<token>.
const CookieName = "agentgo_web"

// Options configures a Server.
type Options struct {
	Service        any    // the object whose exported methods are callable (bridge.AppService)
	Assets         fs.FS  // built frontend (dist/)
	AttachmentsDir string // served under /attachments/
	Token          string // required in the cookie for /api/ipc/*
	// Remote lets non-loopback clients (the mobile app) in. It only takes effect together with
	// a Token, which then must also be presented as X-Access-Key (see remote.go).
	Remote bool
}

// Server is the browser-facing HTTP server.
type Server struct {
	opts Options
	disp *dispatcher
	hub  *hub
	mux  *http.ServeMux
}

// New builds a Server. Wire Publish into bridge.AddEventSink to feed events.
func New(opts Options) *Server {
	s := &Server{opts: opts, disp: newDispatcher(opts.Service), hub: newHub(), mux: http.NewServeMux()}
	s.mux.HandleFunc("/wails/runtime.js", s.serveRuntime)
	s.mux.HandleFunc("/wails/custom.js", s.serveCustom)
	s.mux.HandleFunc("/api/ipc/ping", s.servePing)
	s.mux.HandleFunc("/api/ipc/auth-check", s.requireAuth(s.serveAuthCheck))
	s.mux.HandleFunc("/api/ipc/call", s.requireAuth(s.serveCall))
	s.mux.HandleFunc("/api/ipc/events", s.requireAuth(s.serveEvents))
	s.mux.HandleFunc("/attachments/", s.requireAuth(s.serveAttachment))
	s.mux.HandleFunc("/", s.serveApp)
	return s
}

// Publish forwards one UI event to every open browser tab.
func (s *Server) Publish(name string, data any) { s.hub.publish(name, data) }

// Handler returns the HTTP handler (used by tests and Serve).
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.guard) }

// Serve runs until the listener closes.
func (s *Server) Serve(l net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	err := srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// guard rejects requests that do not address the loopback interface directly.
// That stops DNS-rebinding pages from reaching the API through a hostname they
// control.
func (s *Server) guard(w http.ResponseWriter, r *http.Request) {
	if !s.remote() && !loopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.applyCORS(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
}

func loopbackHost(hostport string) bool {
	u, err := url.Parse("//" + hostport)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) authorized(r *http.Request) bool {
	if s.opts.Token == "" {
		return true
	}
	if s.keyAuthorized(r) {
		return true
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.opts.Token)) == 1
}

// requireAuth admits only same-origin requests that carry the access cookie.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if !s.crossOriginAllowed(r, origin) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request denied"})
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" && !s.keyAuthorized(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-site request denied"})
			return
		}
		if !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serveRuntime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(runtimeJS)
}

func (s *Server) serveCustom(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte("// wails custom\n"))
}

func (s *Server) servePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"app": "agentgo-web"})
}

type callRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

func (s *Server) serveCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); ct != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "application/json required"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "request too large"})
		return
	}
	var req callRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.Method == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expected {method, args}"})
		return
	}
	if !s.disp.has(req.Method) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("unknown method %q", req.Method)})
		return
	}

	// The call's context ends only when the browser aborts the request, never when
	// the call returns: a method may hand its context to work that outlives it.
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-finished:
		}
	}()
	result, err := s.disp.call(ctx, req.Method, req.Args)
	close(finished)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	body, merr := json.Marshal(map[string]any{"result": result})
	if merr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "result is not JSON-encodable: " + merr.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	fl.Flush()

	sub := s.hub.subscribe()
	defer s.hub.unsubscribe(sub)
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-sub.ch:
			if !open {
				return // too slow; the browser reconnects
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			// Drain what is already queued before flushing once.
			for more := true; more; {
				select {
				case m2, open2 := <-sub.ch:
					if !open2 {
						fl.Flush()
						return
					}
					if _, err := fmt.Fprintf(w, "data: %s\n\n", m2); err != nil {
						return
					}
				default:
					more = false
				}
			}
			fl.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// serveAttachment serves one uploaded file. os.Root keeps a symlink or encoded
// separator from reaching outside the attachment directory.
func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/attachments/")
	if s.opts.AttachmentsDir == "" || name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\:") || strings.IndexByte(name, 0) >= 0 {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.opts.AttachmentsDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// Attachments can hold user-supplied markup; keep it from running scripts
	// with the application's origin.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

const unsupportedBrowserPage = `<!doctype html><meta charset="utf-8"><title>AgentGo</title>
<body style="font-family:Segoe UI,Microsoft YaHei,sans-serif;max-width:620px;margin:60px auto;line-height:1.7">
<h2>需要更新的浏览器</h2>
<p>AgentGo 的界面无法在 Internet Explorer 中运行。请安装并用下面任意一个打开本页：</p>
<ul><li>Google Chrome 109（Windows 7 能安装的最后版本）</li><li>Mozilla Firefox 115 ESR（Windows 7 能安装的最后版本）</li></ul>
<p>装好后重新启动 AgentGo，会自动选用它们。</p></body>`

// serveApp serves the built frontend. The first visit carries ?t=<token>; it is
// moved into an HttpOnly cookie and removed from the address bar.
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	if ua := r.UserAgent(); strings.Contains(ua, "MSIE ") || strings.Contains(ua, "Trident/") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, unsupportedBrowserPage)
		return
	}
	if t := r.URL.Query().Get("t"); t != "" && r.Method == http.MethodGet {
		if s.opts.Token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.opts.Token)) == 1 {
			http.SetCookie(w, &http.Cookie{
				Name: CookieName, Value: s.opts.Token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 10 * 365 * 24 * 3600,
			})
		}
		q := r.URL.Query()
		q.Del("t")
		target := r.URL.Path
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if s.opts.Assets == nil {
		http.Error(w, "frontend not embedded", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if st, err := fs.Stat(s.opts.Assets, name); err != nil || st.IsDir() {
		if path.Ext(name) != "" { // a missing asset is a real 404
			http.NotFound(w, r)
			return
		}
		name = "index.html" // client-side route
	}
	f, err := s.opts.Assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), strings.NewReader(string(data)))
}

// LoadOrCreateToken returns the access token stored in dir, creating it on first
// use. Keeping it across runs keeps the browser cookie valid.
func LoadOrCreateToken(dir string) (string, error) {
	file := filepath.Join(dir, "web-token")
	if b, err := os.ReadFile(file); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			return t, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(file, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// PageURL is the address to open: it carries the token for the first visit.
func PageURL(addr net.Addr, token string) string {
	u := "http://" + addr.String() + "/"
	if token != "" {
		u += "?t=" + url.QueryEscape(token)
	}
	return u
}

// IsRunning reports whether an AgentGo web shell already listens on addr.
func IsRunning(addr string) bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get("http://" + addr + "/api/ipc/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		App string `json:"app"`
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&body) == nil && body.App == "agentgo-web"
}
