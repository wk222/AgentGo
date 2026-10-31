package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Browser clients may use the desktop's own origin. Foreign websites must not
// issue model requests or change the user's IDE mode through the asset server.
func allowDesktopAPIRequest(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	host, err := url.Parse("//" + r.Host)
	if err != nil {
		desktopAPIError(w, http.StatusForbidden, "invalid desktop host")
		return false
	}
	hostname := strings.ToLower(host.Hostname())
	ip := net.ParseIP(hostname)
	if hostname != "wails.localhost" && hostname != "localhost" && (ip == nil || !ip.IsLoopback()) {
		desktopAPIError(w, http.StatusForbidden, "non-local desktop host denied")
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.User != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "wails") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			desktopAPIError(w, http.StatusForbidden, "cross-origin request denied")
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		desktopAPIError(w, http.StatusForbidden, "cross-site request denied")
		return false
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	method := http.MethodPost
	if r.URL.Path == "/api/agentgo/status" {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		desktopAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if method == http.MethodPost {
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
		if contentType != "application/json" {
			desktopAPIError(w, http.StatusUnsupportedMediaType, "application/json required")
			return false
		}
	}
	return true
}
func desktopAPIError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}
func decodeDesktopJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	text := strings.TrimSpace(string(raw))
	if err != nil || len(text) == 0 || text[0] != '{' || json.Unmarshal(raw, target) != nil {
		desktopAPIError(w, http.StatusBadRequest, "expected one JSON object within 1 MiB")
		return false
	}
	return true
}

// Uploaded attachments are single files. os.Root prevents a symlink or encoded
// separator from turning this route into a reader outside the attachment root.
func serveDesktopAttachment(w http.ResponseWriter, r *http.Request, dir string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/attachments/")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") || strings.IndexByte(name, 0) >= 0 {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Attachments can contain user-supplied markup. A sandboxed response must not
	// execute scripts with the desktop application's origin privileges.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
