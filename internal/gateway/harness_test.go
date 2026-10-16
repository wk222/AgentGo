package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type lister struct{ Backend }

func (lister) ListHarnesses(context.Context) ([]byte, error) {
	return []byte(`{"harnesses":[{"id":"standard"}],"default":"standard"}`), nil
}

type plain struct{ Backend }

func TestHarnessesEndpoint(t *testing.T) {
	srv := NewServer(Config{}, lister{})
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harnesses", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"default":"standard"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}

	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/harnesses", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST must be rejected, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	NewServer(Config{}, plain{}).http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harnesses", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("a backend without harness support must answer 501, got %d", rec.Code)
	}
}

func TestHarnessesEndpointRequiresToken(t *testing.T) {
	srv := NewServer(Config{Token: "secret"}, lister{})
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harnesses", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
}
