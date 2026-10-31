package apps

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// MaxConcurrentPreviewServers caps the number of active loopback HTTP servers
	// (absorbed from a reference implementation). Oldest server is evicted on overflow.
	MaxConcurrentPreviewServers = 8

	// ExternalPreviewTTL is the lifetime of a standalone external-browser preview handle.
	ExternalPreviewTTL = 30 * time.Minute
)

// PreviewSessionBinding carries the host session credentials injected into window.agentGo.
type PreviewSessionBinding struct {
	AppName   string      `json:"app_name"`
	SessionID string      `json:"session_id"`
	Nonce     string      `json:"nonce"`
	Manifest  AppManifest `json:"manifest"`
}

// PreviewLease describes an active isolated loopback HTTP preview server for one bundle directory.
type PreviewLease struct {
	Handle       string    `json:"handle"`
	AppName      string    `json:"app_name"`
	BundleDir    string    `json:"bundle_dir"`
	Port         int       `json:"port"`
	Origin       string    `json:"origin"`
	EntryURL     string    `json:"entry_url"`
	External     bool      `json:"external,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	LastAccessAt time.Time `json:"last_access_at"`
}

type previewEntry struct {
	lease      PreviewLease
	binding    PreviewSessionBinding
	ln         net.Listener
	srv        *http.Server
	expireStop *time.Timer
}

// PreviewServerPool manages isolated 127.0.0.1:<port>/<handle>/ loopback HTTP servers.
// Each preview runs on its own port so that:
//  1. `<iframe sandbox="allow-scripts allow-same-origin ...">` gets a real HTTP origin
//     (enabling localStorage, sessionStorage, indexedDB, ES module imports, and relative fetch)
//     while remaining cross-origin from the Wails host shell (`window.parent.document` blocked).
//  2. Different InnerApps have port-isolated Web Storage and never overwrite each other's keys.
type PreviewServerPool struct {
	mu      sync.Mutex
	maxConc int
	order   []string // LRU order of handles (oldest first)
	entries map[string]*previewEntry
	byApp   map[string]string // appName (lower) -> active panel handle
}

// NewPreviewServerPool creates a loopback preview server pool.
func NewPreviewServerPool(maxConcurrent int) *PreviewServerPool {
	if maxConcurrent <= 0 {
		maxConcurrent = MaxConcurrentPreviewServers
	}
	return &PreviewServerPool{
		maxConc: maxConcurrent,
		entries: make(map[string]*previewEntry),
		byApp:   make(map[string]string),
	}
}

// AcquirePanelPreview starts (or refreshes) the isolated loopback server for `bundleDir`
// and binds it to the provided `PreviewSessionBinding`.
func (p *PreviewServerPool) AcquirePanelPreview(bundleDir string, binding PreviewSessionBinding) (*PreviewLease, error) {
	bundleDir = strings.TrimSpace(bundleDir)
	if bundleDir == "" {
		return nil, fmt.Errorf("empty bundle directory")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	appKey := strings.ToLower(strings.TrimSpace(binding.AppName))
	if oldHandle, ok := p.byApp[appKey]; ok {
		if existing, exists := p.entries[oldHandle]; exists && existing.lease.BundleDir == bundleDir {
			existing.binding = binding
			existing.lease.LastAccessAt = time.Now()
			p.touchLocked(oldHandle)
			cp := existing.lease
			return &cp, nil
		}
		p.revokeLocked(oldHandle)
	}

	return p.spawnLocked(bundleDir, binding, false, 0)
}

// IssueExternalPreview mints a separate capability handle and loopback server with a 30-minute TTL
// for opening an InnerApp in the user's system browser without tying its lifetime to panel switches.
func (p *PreviewServerPool) IssueExternalPreview(bundleDir string, binding PreviewSessionBinding, ttl time.Duration) (*PreviewLease, error) {
	bundleDir = strings.TrimSpace(bundleDir)
	if bundleDir == "" {
		return nil, fmt.Errorf("empty bundle directory")
	}
	if ttl <= 0 {
		ttl = ExternalPreviewTTL
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spawnLocked(bundleDir, binding, true, ttl)
}

// Revoke shuts down a specific preview handle.
func (p *PreviewServerPool) Revoke(handle string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revokeLocked(strings.TrimSpace(handle))
}

// Close shuts down all active loopback preview servers.
func (p *PreviewServerPool) Close() {
	p.mu.Lock()
	handles := append([]string(nil), p.order...)
	p.mu.Unlock()
	for _, h := range handles {
		p.Revoke(h)
	}
}

// ActiveCount returns the number of running loopback servers.
func (p *PreviewServerPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

func (p *PreviewServerPool) spawnLocked(bundleDir string, binding PreviewSessionBinding, external bool, ttl time.Duration) (*PreviewLease, error) {
	for len(p.order) >= p.maxConc {
		oldest := p.order[0]
		p.revokeLocked(oldest)
	}

	handle, err := generatePreviewHandle()
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen loopback preview: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	now := time.Now()

	lease := PreviewLease{
		Handle:       handle,
		AppName:      binding.AppName,
		BundleDir:    bundleDir,
		Port:         port,
		Origin:       origin,
		EntryURL:     fmt.Sprintf("%s/%s/%s", origin, handle, AppEntryFile),
		External:     external,
		LastAccessAt: now,
	}
	if ttl > 0 {
		lease.ExpiresAt = now.Add(ttl)
	}

	entry := &previewEntry{
		lease:   lease,
		binding: binding,
		ln:      ln,
	}
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serveHTTP(handle, w, r) }),
		ReadHeaderTimeout: 10 * time.Second,
	}
	entry.srv = srv

	if ttl > 0 {
		entry.expireStop = time.AfterFunc(ttl, func() {
			p.Revoke(handle)
		})
	}

	p.entries[handle] = entry
	p.order = append(p.order, handle)
	if !external && binding.AppName != "" {
		p.byApp[strings.ToLower(strings.TrimSpace(binding.AppName))] = handle
	}

	go func() {
		_ = srv.Serve(ln)
	}()

	cp := entry.lease
	return &cp, nil
}

func (p *PreviewServerPool) revokeLocked(handle string) bool {
	ent, ok := p.entries[handle]
	if !ok {
		return false
	}
	delete(p.entries, handle)
	for i, h := range p.order {
		if h == handle {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	for k, v := range p.byApp {
		if v == handle {
			delete(p.byApp, k)
		}
	}
	if ent.expireStop != nil {
		ent.expireStop.Stop()
	}
	go func(s *http.Server, l net.Listener) {
		_ = l.Close()
		_ = s.Close()
	}(ent.srv, ent.ln)
	return true
}

func (p *PreviewServerPool) touchLocked(handle string) {
	for i, h := range p.order {
		if h == handle {
			p.order = append(p.order[:i], p.order[i+1:]...)
			p.order = append(p.order, handle)
			return
		}
	}
}

func (p *PreviewServerPool) serveHTTP(expectedHandle string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p.mu.Lock()
	ent, ok := p.entries[expectedHandle]
	if !ok {
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if !ent.lease.ExpiresAt.IsZero() && time.Now().After(ent.lease.ExpiresAt) {
		p.revokeLocked(expectedHandle)
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	ent.lease.LastAccessAt = time.Now()
	bundleDir := ent.lease.BundleDir
	binding := ent.binding
	p.mu.Unlock()

	handleSegment, relPath, ok := parsePreviewRequestPath(r.URL.Path, r.Header.Get("Referer"), expectedHandle)
	if !ok || handleSegment != expectedHandle {
		http.NotFound(w, r)
		return
	}

	setPreviewSecurityHeaders(w)

	if relPath == "agentgo-app-helpers.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(BuildAgentGoHelperJS(binding)))
		return
	}

	data, mime, err := ReadBundleFile(bundleDir, relPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if strings.EqualFold(filepath.Ext(relPath), ".html") || strings.EqualFold(relPath, AppEntryFile) {
		injected := injectLoopbackHelperHTML(string(data), expectedHandle, binding)
		data = []byte(injected)
		mime = "text/html; charset=utf-8"
	}

	w.Header().Set("Content-Type", mime)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

func setPreviewSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
}

// parsePreviewRequestPath extracts `(handle, relPath)` from `/<handle>/<relPath>`.
// Also supports root-relative requests (like `/agentgo-app-helpers.js` or `/style.css`)
// when the same-origin `Referer` header carries `/<expectedHandle>/...`.
func parsePreviewRequestPath(rawPath, referer, expectedHandle string) (handle, relPath string, ok bool) {
	if strings.Contains(rawPath, "\\") || strings.Contains(rawPath, "\x00") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(rawPath, "/")
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 2)
	first := parts[0]
	var rest string
	if len(parts) > 1 {
		rest = parts[1]
	}

	if first == expectedHandle {
		if rest == "" || strings.HasSuffix(rawPath, "/") {
			rest = strings.TrimSuffix(rest, "/")
			if rest == "" {
				rest = AppEntryFile
			} else {
				rest = rest + "/" + AppEntryFile
			}
		}
		if !isSafeRelativeSegments(rest) {
			return "", "", false
		}
		return expectedHandle, rest, true
	}

	// Root-relative asset fallback when Referer on the same loopback server proves capability possession
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			refTrim := strings.TrimPrefix(refURL.Path, "/")
			refParts := strings.SplitN(refTrim, "/", 2)
			if len(refParts) >= 1 && refParts[0] == expectedHandle {
				if isSafeRelativeSegments(trimmed) {
					return expectedHandle, trimmed, true
				}
			}
		}
	}
	return "", "", false
}

func isSafeRelativeSegments(rel string) bool {
	if rel == "" || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func generatePreviewHandle() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "p_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func injectLoopbackHelperHTML(rawHTML, handle string, binding PreviewSessionBinding) string {
	inlineTag := "<script>\n" + BuildAgentGoHelperJS(binding) + "\n</script>"
	// Replace any existing placeholder script tag or inject before </head>
	if idx := indexFold(rawHTML, "agentgo-app-helpers"); idx >= 0 {
		// Also rewrite `/agentgo-app-helpers.js` to `./agentgo-app-helpers.js` and prepend inline helper
		rawHTML = strings.ReplaceAll(rawHTML, `"/agentgo-app-helpers.js"`, `"/`+handle+`/agentgo-app-helpers.js"`)
		rawHTML = strings.ReplaceAll(rawHTML, `'/agentgo-app-helpers.js'`, `'/`+handle+`/agentgo-app-helpers.js'`)
	}
	if i := indexFold(rawHTML, "</head>"); i >= 0 {
		return rawHTML[:i] + inlineTag + rawHTML[i:]
	}
	return inlineTag + rawHTML
}

// BuildAgentGoHelperJS generates the `window.agentGo` bridge script injected into
// loopback-served InnerApp pages so `postMessage` IPC works identically to `srcdoc`.
func BuildAgentGoHelperJS(binding PreviewSessionBinding) string {
	appNameJSON, _ := json.Marshal(binding.AppName)
	sessionIDJSON, _ := json.Marshal(binding.SessionID)
	nonceJSON, _ := json.Marshal(binding.Nonce)
	manifestJSON, _ := json.Marshal(binding.Manifest)
	manifestSafe := strings.ReplaceAll(string(manifestJSON), "</script", `<\/script`)

	return fmt.Sprintf(`(function () {
  if (window.__agentGoInitialized) return;
  window.__agentGoInitialized = true;
  const appName = %s;
  const sessionId = %s;
  const nonce = %s;
  const manifest = %s;
  const actionSet = new Set((manifest.actions || []).map(function (a) { return String(a.name || a); }));
  const openPolicy = manifest.export_policy === 'open';
  let seq = 0;
  const pending = new Map();
  window.addEventListener('error', function (ev) {
    window.parent.postMessage({
      type: 'agentgo:inner-app-runtime-error',
      app: appName,
      session_id: sessionId,
      error: (ev && ev.message) ? String(ev.message) : 'Unknown runtime error'
    }, '*');
  });
  window.addEventListener('unhandledrejection', function (ev) {
    window.parent.postMessage({
      type: 'agentgo:inner-app-runtime-error',
      app: appName,
      session_id: sessionId,
      error: (ev && ev.reason) ? String(ev.reason.message || ev.reason) : 'Unhandled promise rejection'
    }, '*');
  });
  window.addEventListener('message', function (ev) {
    const msg = ev.data || {};
    if (msg.type !== 'agentgo:inner-app-api:result') return;
    if (msg.session_id && msg.session_id !== sessionId) return;
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    if (msg.ok) p.resolve(msg.result);
    else p.reject(new Error(msg.error || 'inner app call failed'));
  });
  window.agentGo = {
    appName: appName,
    sessionId: sessionId,
    manifest: manifest,
    actions: manifest.actions || [],
    canCall: function (action) {
      action = String(action || '');
      return openPolicy || action === 'info' || actionSet.has(action);
    },
    apiCall: function (action, payload) {
      action = String(action || '');
      if (!this.canCall(action)) {
        return Promise.reject(new Error('inner app action is not exported: ' + action));
      }
      const id = 'call_' + (++seq) + '_' + Date.now();
      window.parent.postMessage({
        type: 'agentgo:inner-app-api',
        id: id,
        app: appName,
        session_id: sessionId,
        nonce: nonce,
        action: action,
        payload: payload || {}
      }, '*');
      return new Promise(function (resolve, reject) {
        pending.set(id, { resolve, reject });
        setTimeout(function () {
          if (!pending.has(id)) return;
          pending.delete(id);
          reject(new Error('inner app call timeout'));
        }, 15000);
      });
    },
    chat: function (input) { return this.apiCall('chat', { input: input }); },
    hostCall: function (method, params) {
      const id = 'host_' + (++seq) + '_' + Date.now();
      window.parent.postMessage({
        type: 'agentgo:host-sdk',
        id: id,
        app: appName,
        session_id: sessionId,
        nonce: nonce,
        method: String(method || ''),
        params: params || {}
      }, '*');
      return new Promise(function (resolve, reject) {
        pending.set(id, { resolve, reject });
        setTimeout(function () {
          if (!pending.has(id)) return;
          pending.delete(id);
          reject(new Error('host SDK call timeout: ' + method));
        }, 20000);
      });
    },
    fs: {
      list: function (relPath) { return window.agentGo.hostCall('fs.list', { path: relPath || '' }); },
      read: function (relPath) { return window.agentGo.hostCall('fs.read', { path: relPath || '' }); },
      write: function (relPath, content) { return window.agentGo.hostCall('fs.write', { path: relPath || '', content: String(content ?? '') }); }
    },
    workflow: {
      list: function () { return window.agentGo.hostCall('workflow.list', {}); },
      run: function (workflowId, input) { return window.agentGo.hostCall('workflow.run', { id: workflowId, input: input || {} }); }
    },
    memory: {
      search: function (query) { return window.agentGo.hostCall('memory.search', { query: String(query || '') }); }
    },
    agent: {
      ask: function (prompt) { return window.agentGo.hostCall('agent.ask', { prompt: String(prompt || '') }); }
    },
    ui: {
      notify: function (message, level) { return window.agentGo.hostCall('ui.notify', { message: String(message || ''), level: level || 'info' }); }
    }
  };
  window.agentgo = window.agentGo;
  window.parent.postMessage({ type: 'agentgo:inner-app-ready', app: appName, session_id: sessionId }, '*');
})();`, string(appNameJSON), string(sessionIDJSON), string(nonceJSON), manifestSafe)
}
