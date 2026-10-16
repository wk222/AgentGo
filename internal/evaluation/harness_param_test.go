package evaluation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAgentSendsHarnessOnlyWhenSet(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = map[string]string{}
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: done\ndata: {\"content\":\"ok\"}\n\n"))
	}))
	defer srv.Close()

	if _, _, err := (&HTTPAgent{BaseURL: srv.URL, Harness: "minimal"}).GenerateMeasured(context.Background(), "s", "q"); err != nil {
		t.Fatal(err)
	}
	if got["harness"] != "minimal" || got["session_id"] != "s" || got["message"] != "q" {
		t.Errorf("request body = %v", got)
	}

	if _, _, err := (&HTTPAgent{BaseURL: srv.URL}).GenerateMeasured(context.Background(), "s", "q"); err != nil {
		t.Fatal(err)
	}
	if _, has := got["harness"]; has {
		t.Errorf("harness must be omitted when not set: %v", got)
	}
}
