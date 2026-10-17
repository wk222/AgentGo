package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type resolver struct {
	Backend
	gotID      string
	gotApprove bool
	err        error
}

func (r *resolver) ResolveApproval(_ context.Context, id string, approve bool, emit func(string, []byte) error) (*ChatResult, error) {
	r.gotID, r.gotApprove = id, approve
	if r.err != nil {
		return nil, r.err
	}
	_ = emit("chunk", []byte(`{"delta":"wrote it"}`))
	_ = emit("interrupt", []byte(`{"approval_id":"next-1","tool_name":"execute_bash"}`))
	return &ChatResult{Content: "wrote it"}, nil
}

func postApproval(t *testing.T, b Backend, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer(Config{}, b).http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestApprovalsEndpointStreamsTheContinuedRun(t *testing.T) {
	r := &resolver{}
	rec := postApproval(t, r, "/api/v1/approvals/ap-9", `{"approve":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if r.gotID != "ap-9" || !r.gotApprove {
		t.Fatalf("resolver got id=%q approve=%v", r.gotID, r.gotApprove)
	}
	body := rec.Body.String()
	for _, want := range []string{"event: chunk", "wrote it", "event: interrupt", "next-1", "event: done"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream is missing %q:\n%s", want, body)
		}
	}
}

func TestApprovalsEndpointDeny(t *testing.T) {
	r := &resolver{}
	postApproval(t, r, "/api/v1/approvals/ap-1", `{"approve":false}`)
	if r.gotID != "ap-1" || r.gotApprove {
		t.Fatalf("deny not forwarded: id=%q approve=%v", r.gotID, r.gotApprove)
	}
}

func TestApprovalsEndpointReportsFailureAsErrorEvent(t *testing.T) {
	rec := postApproval(t, &resolver{err: errors.New("no such approval")}, "/api/v1/approvals/x", `{"approve":true}`)
	if body := rec.Body.String(); !strings.Contains(body, "event: error") || !strings.Contains(body, "no such approval") {
		t.Fatalf("expected an error event:\n%s", body)
	}
}

func TestApprovalsEndpointRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		backend                  Backend
		want                     int
	}{
		{"missing id", http.MethodPost, "/api/v1/approvals/", `{"approve":true}`, &resolver{}, http.StatusBadRequest},
		{"nested path", http.MethodPost, "/api/v1/approvals/a/b", `{"approve":true}`, &resolver{}, http.StatusBadRequest},
		{"bad body", http.MethodPost, "/api/v1/approvals/a", `nope`, &resolver{}, http.StatusBadRequest},
		{"wrong method", http.MethodGet, "/api/v1/approvals/a", ``, &resolver{}, http.StatusMethodNotAllowed},
		{"unsupported backend", http.MethodPost, "/api/v1/approvals/a", `{"approve":true}`, plain{}, http.StatusNotImplemented},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		NewServer(Config{}, c.backend).http.Handler.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
	}
}
