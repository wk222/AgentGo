package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeThreads struct {
	Backend
	bound    map[string]string
	event    string
	created  bool
	unbound  string
	delivErr error
}

func (f *fakeThreads) DeliverThread(_ context.Context, key, source, text string, create bool) (string, error) {
	if f.delivErr != nil {
		return "", f.delivErr
	}
	f.event, f.created = key+"|"+source+"|"+text, create
	return "sess-1", nil
}
func (f *fakeThreads) ListThreads(context.Context) ([]byte, error) { return []byte(`[]`), nil }
func (f *fakeThreads) BindThread(_ context.Context, key, sid, _ string) error {
	f.bound[key] = sid
	return nil
}
func (f *fakeThreads) UnbindThread(_ context.Context, key string) error {
	f.unbound = key
	return nil
}

func doThreads(f *fakeThreads, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	NewServer(Config{}, f).http.Handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestThreadEventReachesTheBackendWithItsKey(t *testing.T) {
	f := &fakeThreads{bound: map[string]string{}}
	// The key has '/' and '#': it must travel in the body.
	rec := doThreads(f, http.MethodPost, "/api/v1/threads/events",
		`{"key":"github:acme/app#12","text":"LGTM","source":"github","create":true}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "sess-1") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if f.event != "github:acme/app#12|github|LGTM" || !f.created {
		t.Fatalf("backend got %q create=%v", f.event, f.created)
	}
}

func TestThreadEventForUnknownThreadIs404AndBadInputIs400(t *testing.T) {
	f := &fakeThreads{bound: map[string]string{}, delivErr: fmt.Errorf("%w: k", ErrThreadNotFound)}
	if rec := doThreads(f, http.MethodPost, "/api/v1/threads/events", `{"key":"k","text":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown thread: %d", rec.Code)
	}
	if rec := doThreads(f, http.MethodPost, "/api/v1/threads/events", `{"key":"k"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing text: %d", rec.Code)
	}
	f.delivErr = errors.New("event text is required")
	if rec := doThreads(f, http.MethodPost, "/api/v1/threads/events", `{"key":"k","text":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("backend refusal: %d", rec.Code)
	}
	if rec := doThreads(f, http.MethodGet, "/api/v1/threads/events", ``); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestBindListAndUnbindThreads(t *testing.T) {
	f := &fakeThreads{bound: map[string]string{}}
	if rec := doThreads(f, http.MethodPost, "/api/v1/threads", `{"key":"mail:<a@b>","session_id":"s9","title":"t"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("bind: %d", rec.Code)
	}
	if f.bound["mail:<a@b>"] != "s9" {
		t.Fatalf("not bound: %v", f.bound)
	}
	if rec := doThreads(f, http.MethodGet, "/api/v1/threads", ``); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if rec := doThreads(f, http.MethodDelete, "/api/v1/threads?key=mail%3A%3Ca%40b%3E", ``); rec.Code != http.StatusNoContent || f.unbound != "mail:<a@b>" {
		t.Fatalf("unbind: %d %q", rec.Code, f.unbound)
	}
	if rec := doThreads(f, http.MethodDelete, "/api/v1/threads", ``); rec.Code != http.StatusBadRequest {
		t.Fatalf("unbind without a key: %d", rec.Code)
	}
}

func TestThreadRoutesNeedABackendThatSupportsThem(t *testing.T) {
	rec := httptest.NewRecorder()
	NewServer(Config{}, plain{}).http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/threads/events", strings.NewReader(`{"key":"k","text":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a backend without threads should not expose the route, got %d", rec.Code)
	}
}
