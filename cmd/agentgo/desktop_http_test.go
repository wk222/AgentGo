package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopAPIRejectsBrowserCrossOriginAndBadRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, origin, ctype, body, site string
		status                                        int
	}{
		{"foreign", "POST", "mode", "https://evil.example", "application/json", `{}`, "", 403},
		{"opaque", "POST", "mode", "null", "application/json", `{}`, "", 403},
		{"crosssite", "POST", "mode", "", "application/json", `{}`, "cross-site", 403},
		{"method", "GET", "mode", "", "", "", "", 405},
		{"form", "POST", "mode", "", "text/plain", `{}`, "", 415},
		{"malformed", "POST", "mode", "", "application/json", `{`, "", 400},
		{"multiple", "POST", "mode", "", "application/json", `{} {}`, "", 400},
		{"null", "POST", "mode", "", "application/json", `null`, "", 400},
		{"array", "POST", "mode", "", "application/json", `[]`, "", 400},
		{"large", "POST", "mode", "", "application/json", `{"v":"` + strings.Repeat("x", 1024*1024) + `"}`, "", 400},
		{"preflight", "OPTIONS", "mode", "http://wails.localhost", "", "", "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://wails.localhost/api/agentgo/"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", tc.ctype)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			w := httptest.NewRecorder()
			// A rejected request must never reach the application service.
			handleAgentGoAPI(nil, w, req)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") == "*" {
				t.Fatal("wildcard CORS leaked")
			}
		})
	}
	req := httptest.NewRequest("POST", "http://attacker.example/api/agentgo/mode", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAgentGoAPI(nil, w, req)
	if w.Code != 403 {
		t.Fatal("DNS-rebinding host accepted", w.Code)
	}
}
func TestDesktopAPIAcceptsLocalJSONAndRejectsUnknownMethod(t *testing.T) {
	for _, host := range []string{"wails.localhost", "127.0.0.1:1234", "localhost:1234", "[::1]:1234"} {
		req := httptest.NewRequest("POST", "http://"+host+"/api/agentgo/mode", strings.NewReader(`{"modeId":"code"}`))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Origin", "http://"+host)
		w := httptest.NewRecorder()
		if !allowDesktopAPIRequest(w, req) {
			t.Fatal(host, w.Code, w.Body.String())
		}
		var target struct {
			ModeID string `json:"modeId"`
		}
		if !decodeDesktopJSON(w, req, &target) || target.ModeID != "code" {
			t.Fatal(target)
		}
	}
	req := httptest.NewRequest("DELETE", "http://wails.localhost/api/agentgo/status", nil)
	w := httptest.NewRecorder()
	handleAgentGoAPI(nil, w, req)
	if w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatal(w.Code)
	}
}
func TestDesktopAttachmentBoundaryAndActiveContentSandbox(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("allowed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/attachments/..%5csecret", "/attachments/../secret", "/attachments/C:%5csecret", "/attachments/folder", "/attachments/"} {
		req := httptest.NewRequest("GET", "http://wails.localhost"+path, nil)
		w := httptest.NewRecorder()
		serveDesktopAttachment(w, req, root)
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	req := httptest.NewRequest("GET", "http://wails.localhost/attachments/ok.txt", nil)
	w := httptest.NewRecorder()
	serveDesktopAttachment(w, req, root)
	if w.Code != 200 || w.Body.String() != "allowed" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Body.String())
	}
	req = httptest.NewRequest("POST", "http://wails.localhost/attachments/ok.txt", nil)
	w = httptest.NewRecorder()
	serveDesktopAttachment(w, req, root)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
func TestDesktopAttachmentRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	req := httptest.NewRequest("GET", "http://wails.localhost/attachments/link.txt", nil)
	w := httptest.NewRecorder()
	serveDesktopAttachment(w, req, root)
	if w.Code != 404 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Code, w.Body.String())
	}
}
