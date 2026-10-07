package crushproto

import (
	"context"
	"testing"
	"time"
)

type mockLSPProvider struct {
	states   map[string]LSPClientInfo
	diags    map[string]any
	started  []string
	stopped  int
	listener func(evType, name string, state int, diagCount int)
}

func (m *mockLSPProvider) LSPStates(workDir string) map[string]LSPClientInfo {
	return m.states
}

func (m *mockLSPProvider) LSPDiagnostics(workDir, lspName string) any {
	return m.diags[lspName]
}

func (m *mockLSPProvider) StartLSP(ctx context.Context, workDir, path string) {
	m.started = append(m.started, path)
}

func (m *mockLSPProvider) StopAllLSPs(ctx context.Context, workDir string) {
	m.stopped++
}

func (m *mockLSPProvider) SetLSPEventListener(workDir string, fn func(evType, name string, state int, diagCount int)) {
	m.listener = fn
}

func TestLSPRoutesAndSSEEvents(t *testing.T) {
	mock := &mockLSPProvider{
		states: map[string]LSPClientInfo{
			"gopls": {
				Name:            "gopls",
				State:           2,
				DiagnosticCount: 3,
				ConnectedAt:     time.Now().Truncate(time.Second),
			},
		},
		diags: map[string]any{
			"gopls": map[string]any{
				"file:///main.go": []any{
					map[string]any{"message": "syntax error"},
				},
			},
		},
	}

	h := newHarness(t, nil, func(o *Options) {
		o.LSPProvider = mock
	})

	// 1. GET /v1/workspaces/{id}/lsps
	var states map[string]LSPClientInfo
	status := h.do("GET", "/v1/workspaces/"+h.ws+"/lsps", nil, &states)
	if status != 200 || len(states) != 1 || states["gopls"].DiagnosticCount != 3 {
		t.Fatalf("GET /lsps = %d, %+v", status, states)
	}

	// 2. GET /v1/workspaces/{id}/lsps/gopls/diagnostics
	var diags map[string]any
	status = h.do("GET", "/v1/workspaces/"+h.ws+"/lsps/gopls/diagnostics", nil, &diags)
	if status != 200 || len(diags) != 1 {
		t.Fatalf("GET /diagnostics = %d, %+v", status, diags)
	}

	// 3. POST /v1/workspaces/{id}/lsps/start
	status = h.do("POST", "/v1/workspaces/"+h.ws+"/lsps/start", map[string]string{"path": "/test/main.go"}, nil)
	if status != 200 || len(mock.started) != 1 || mock.started[0] != "/test/main.go" {
		t.Fatalf("POST /lsps/start = %d, started=%v", status, mock.started)
	}

	// 4. Trigger event listener -> expect SSE event
	if mock.listener == nil {
		t.Fatal("listener was not registered")
	}
	mock.listener("diagnostics_changed", "gopls", 2, 1)

	ev, _ := h.until(func(e sseEvent) bool {
		return e.Kind == "lsp_event" && e.Op == "updated"
	})
	if ev.Kind != "lsp_event" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}
