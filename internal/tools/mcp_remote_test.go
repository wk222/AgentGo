package tools

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// headerSpy records the headers of every request before handing it on.
type headerSpy struct {
	mu   sync.Mutex
	seen []http.Header
	next http.Handler
}

func (h *headerSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.seen = append(h.seen, r.Header.Clone())
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *headerSpy) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

// sawHeader reports whether any request carried name: want.
func (h *headerSpy) sawHeader(name, want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, hdr := range h.seen {
		if hdr.Get(name) == want {
			return true
		}
	}
	return false
}

func echoMCPServer(label string) *server.MCPServer {
	s := server.NewMCPServer(label, "1.0.0")
	s.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("echo the text back"),
			mcp.WithString("text", mcp.Required()),
			mcp.WithReadOnlyHintAnnotation(true)),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(label + ":" + req.GetString("text", "")), nil
		})
	return s
}

// httpMCP serves a Streamable HTTP MCP server at <url>/mcp.
func httpMCP(t *testing.T, label string) (string, *headerSpy) {
	t.Helper()
	spy := &headerSpy{next: server.NewStreamableHTTPServer(echoMCPServer(label))}
	ts := httptest.NewServer(spy)
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts.URL + "/mcp", spy
}

// sseMCP serves an SSE MCP server at <url>/sse.
func sseMCP(t *testing.T, label string) (string, *headerSpy) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	sse := server.NewSSEServer(echoMCPServer(label), server.WithBaseURL("http://"+ts.Listener.Addr().String()))
	spy := &headerSpy{next: sse}
	ts.Config.Handler = spy
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts.URL + "/sse", spy
}

func callEcho(t *testing.T, r *Registry, name, text string) string {
	t.Helper()
	base, ok := r.Get(name)
	if !ok {
		t.Fatalf("tool %s is not registered", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := base.(tool.InvokableTool).InvokableRun(ctx, `{"text":"`+text+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func loadRemote(t *testing.T, r *Registry, name, rawURL, transport string, headers map[string]string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.LoadRemoteMCPServer(ctx, name, rawURL, transport, headers)
}

func TestRemoteMCPStreamableHTTPSendsHeadersAndHidesThem(t *testing.T) {
	t.Setenv("AGENTGO_TEST_MCP_TOKEN", "s3cret-token")
	r := contractRegistry(t)
	addr, spy := httpMCP(t, "remote")

	err := loadRemote(t, r, "svc", addr+"?key=query-secret", "http", map[string]string{
		"Authorization": "Bearer ${AGENTGO_TEST_MCP_TOKEN}",
		"X-Team":        "red",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !spy.sawHeader("Authorization", "Bearer s3cret-token") || !spy.sawHeader("X-Team", "red") {
		t.Fatalf("server did not receive the configured headers: %v", spy.seen)
	}
	if got := callEcho(t, r, "svc_echo", "hi"); !strings.Contains(got, "remote:hi") {
		t.Fatalf("unexpected tool output %q", got)
	}

	raw, err := json.Marshal(r.ListMCPServers())
	if err != nil {
		t.Fatal(err)
	}
	listing := string(raw)
	for _, secret := range []string{"s3cret-token", "query-secret", "Authorization"} {
		if strings.Contains(listing, secret) {
			t.Fatalf("listing leaks %q: %s", secret, listing)
		}
	}
	servers := r.ListMCPServers()
	if len(servers) != 1 || servers[0].Transport != "http" || !strings.HasSuffix(servers[0].URL, "/mcp") || servers[0].Command != "" {
		t.Fatalf("unexpected summary %+v", servers)
	}
}

func TestRemoteMCPSSE(t *testing.T) {
	r := contractRegistry(t)
	addr, spy := sseMCP(t, "legacy")
	if err := loadRemote(t, r, "old", addr, "sse", map[string]string{"X-Key": "abc"}); err != nil {
		t.Fatal(err)
	}
	if !spy.sawHeader("X-Key", "abc") {
		t.Fatal("SSE server did not receive the header")
	}
	if got := callEcho(t, r, "old_echo", "yo"); !strings.Contains(got, "legacy:yo") {
		t.Fatalf("unexpected tool output %q", got)
	}
	if s := r.ListMCPServers(); len(s) != 1 || s[0].Transport != "sse" {
		t.Fatalf("unexpected summary %+v", s)
	}
}

func TestRemoteMCPAutoDetectsTransport(t *testing.T) {
	r := contractRegistry(t)
	httpAddr, _ := httpMCP(t, "modern")
	sseAddr, _ := sseMCP(t, "legacy")
	if err := loadRemote(t, r, "modern", httpAddr, "", nil); err != nil {
		t.Fatalf("streamable http server: %v", err)
	}
	if err := loadRemote(t, r, "legacy", sseAddr, "", nil); err != nil {
		t.Fatalf("sse server: %v", err)
	}
	got := map[string]string{}
	for _, s := range r.ListMCPServers() {
		got[s.Name] = s.Transport
	}
	if got["modern"] != "http" || got["legacy"] != "sse" {
		t.Fatalf("transports %v", got)
	}
	if out := callEcho(t, r, "legacy_echo", "x"); !strings.Contains(out, "legacy:x") {
		t.Fatalf("unexpected tool output %q", out)
	}
}

func TestRemoteMCPMissingEnvNamesTheVariableAndNeverConnects(t *testing.T) {
	os.Unsetenv("AGENTGO_TEST_MCP_ABSENT")
	t.Setenv("AGENTGO_TEST_MCP_EMPTY", "")
	r := contractRegistry(t)
	addr, spy := httpMCP(t, "remote")
	err := loadRemote(t, r, "svc", addr, "http", map[string]string{
		"Authorization": "Bearer ${AGENTGO_TEST_MCP_ABSENT}",
		"X-Other":       "${AGENTGO_TEST_MCP_EMPTY}",
	})
	if err == nil {
		t.Fatal("expected an error for unset variables")
	}
	for _, want := range []string{"AGENTGO_TEST_MCP_ABSENT", "AGENTGO_TEST_MCP_EMPTY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %s: %v", want, err)
		}
	}
	if spy.count() != 0 {
		t.Fatal("a request went out although the configuration was incomplete")
	}
}

func TestRemoteMCPErrorsAndLogsDoNotLeakTokens(t *testing.T) {
	t.Setenv("AGENTGO_TEST_MCP_TOKEN", "bearer-secret-value")
	var logs strings.Builder
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	r := contractRegistry(t)
	// Nothing listens on port 1, so the failure message embeds the request URL.
	err := loadRemote(t, r, "dead", "http://127.0.0.1:1/mcp?key=query-secret", "", map[string]string{
		"Authorization": "Bearer ${AGENTGO_TEST_MCP_TOKEN}",
	})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	for _, secret := range []string{"query-secret", "bearer-secret-value"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("%q leaked.\nerror: %v\nlog: %s", secret, err, logs.String())
		}
	}

	// The transports' own log lines go through the same filter.
	l := mcpTransportLogger{secrets: []string{"bearer-secret-value"}}
	l.Errorf("failed to send close request: Delete %q: refused, auth was %s", "http://h/mcp?key=query-secret", "bearer-secret-value")
	if strings.Contains(logs.String(), "query-secret") || strings.Contains(logs.String(), "bearer-secret-value") {
		t.Fatalf("transport logger leaked: %s", logs.String())
	}
}

func TestRemoteMCPUnauthorizedExplainsWhatToDo(t *testing.T) {
	r := contractRegistry(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	err := loadRemote(t, r, "locked", ts.URL, "", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Authorization header") {
		t.Fatalf("error does not say how to fix it: %v", err)
	}
	if len(r.ListMCPServers()) != 0 {
		t.Fatal("a failed server must not be registered")
	}
}

func TestRemoteMCPHangingServerIsAbortedByTheContext(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// An SSE stream that never announces its endpoint.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })

	for _, transport := range []string{"http", "sse"} {
		r := contractRegistry(t)
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		err := r.LoadRemoteMCPServer(ctx, "stuck", ts.URL, transport, nil)
		cancel()
		if err == nil {
			t.Fatalf("%s: expected a timeout", transport)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("%s: gave up after %v, the context was ignored", transport, elapsed)
		}
	}
}

// TestRemoteMCPLive talks to a real hosted server. It only runs when
// AGENTGO_LIVE_MCP_URL is set, e.g. https://mcp.deepwiki.com/mcp.
func TestRemoteMCPLive(t *testing.T) {
	addr := os.Getenv("AGENTGO_LIVE_MCP_URL")
	if addr == "" {
		t.Skip("set AGENTGO_LIVE_MCP_URL to run")
	}
	r := contractRegistry(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.LoadRemoteMCPServer(ctx, "live", addr, os.Getenv("AGENTGO_LIVE_MCP_TRANSPORT"), nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.ListMCPServers() {
		t.Logf("%s via %s at %s: %v", s.Name, s.Transport, s.URL, s.Tools)
		if len(s.Tools) == 0 {
			t.Fatal("the server exposes no tools")
		}
	}
}

func TestMCPServerConfigTarget(t *testing.T) {
	cases := []struct {
		name      string
		cfg       MCPServerConfig
		wantErr   string
		remote    bool
		transport string
	}{
		{name: "stdio by command", cfg: MCPServerConfig{Command: "node"}},
		{name: "remote by url", cfg: MCPServerConfig{URL: "https://x/mcp"}, remote: true},
		{name: "streamable-http spelling", cfg: MCPServerConfig{URL: "https://x/mcp", Type: "streamable-http"}, remote: true, transport: "http"},
		{name: "streamableHttp spelling", cfg: MCPServerConfig{URL: "https://x/mcp", Type: "streamableHttp"}, remote: true, transport: "http"},
		{name: "sse", cfg: MCPServerConfig{URL: "https://x/sse", Type: "SSE"}, remote: true, transport: "sse"},
		{name: "explicit stdio", cfg: MCPServerConfig{Command: "node", Type: "stdio"}},
		{name: "both without a type", cfg: MCPServerConfig{Command: "node", URL: "https://x"}, wantErr: "not both"},
		{name: "neither", cfg: MCPServerConfig{}, wantErr: "required"},
		{name: "http without url", cfg: MCPServerConfig{Command: "node", Type: "http"}, wantErr: "url is required"},
		{name: "stdio without command", cfg: MCPServerConfig{URL: "https://x", Type: "stdio"}, wantErr: "command is required"},
		{name: "unknown transport", cfg: MCPServerConfig{URL: "https://x", Type: "carrier-pigeon"}, wantErr: "unsupported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.cfg.target()
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want error containing %q, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.remote() != c.remote || got.Transport != c.transport {
				t.Fatalf("got remote=%v transport=%q", got.remote(), got.Transport)
			}
		})
	}
}

func TestResolveMCPRemoteValidation(t *testing.T) {
	bad := []struct {
		name    string
		url     string
		headers map[string]string
	}{
		{"ftp scheme", "ftp://host/x", nil},
		{"relative", "/mcp", nil},
		{"no host", "https:///mcp", nil},
		{"header name with space", "https://h/mcp", map[string]string{"Bad Name": "v"}},
		{"header value with newline", "https://h/mcp", map[string]string{"X": "a\r\nInjected: 1"}},
	}
	for _, c := range bad {
		if _, _, err := resolveMCPRemote(c.url, c.headers); err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
	if got := redactMCPURL("https://user:pw@host:8443/path/mcp?token=abc#frag"); got != "https://host:8443/path/mcp" {
		t.Fatalf("redactMCPURL = %q", got)
	}
}

func TestAutoLoadMCPServersLoadsRemoteEntries(t *testing.T) {
	t.Setenv("AGENTGO_TEST_MCP_TOKEN", "tok")
	addr, spy := httpMCP(t, "auto")
	ws := t.TempDir()
	cfg := map[string]any{"mcpServers": map[string]any{
		"hosted":   map[string]any{"url": addr, "headers": map[string]string{"Authorization": "Bearer ${AGENTGO_TEST_MCP_TOKEN}"}},
		"off":      map[string]any{"url": addr, "disabled": true},
		"ambigous": map[string]any{"command": "node", "url": addr},
		"broken":   map[string]any{"url": "http://127.0.0.1:1/mcp"},
	}}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(ws, "mcp_servers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := contractRegistry(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	AutoLoadMCPServers(ctx, r, ws)

	servers := r.ListMCPServers()
	if len(servers) != 1 || servers[0].Name != "hosted" || servers[0].Transport != "http" {
		t.Fatalf("expected only the hosted server, got %+v", servers)
	}
	if !spy.sawHeader("Authorization", "Bearer tok") {
		t.Fatal("the configured header never reached the server")
	}
	if got := callEcho(t, r, "hosted_echo", "a"); !strings.Contains(got, "auto:a") {
		t.Fatalf("unexpected tool output %q", got)
	}
}

func TestMCPManagerRegistersRemoteServerAndKeepsSecretsOutOfListings(t *testing.T) {
	t.Setenv("AGENTGO_TEST_MCP_TOKEN", "manager-secret")
	addr, spy := httpMCP(t, "managed")
	ws := t.TempDir()
	r := contractRegistry(t)
	if err := RegisterMCPManagerTool(r, ws); err != nil {
		t.Fatal(err)
	}
	manage := func(args map[string]any) mcpManagerOutput {
		t.Helper()
		base, ok := r.Get("manage_mcp_server")
		if !ok {
			t.Fatal("manage_mcp_server is not registered")
		}
		raw, _ := json.Marshal(args)
		out, err := base.(tool.InvokableTool).InvokableRun(context.Background(), string(raw))
		if err != nil {
			t.Fatal(err)
		}
		var res mcpManagerOutput
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return res
	}

	if res := manage(map[string]any{"action": "register", "name": "both", "command": "node", "url": addr}); res.Success {
		t.Fatal("command and url together must be refused")
	}
	if res := manage(map[string]any{"action": "register", "name": "none"}); res.Success {
		t.Fatal("neither command nor url must be refused")
	}

	res := manage(map[string]any{
		"action": "register", "name": "svc", "url": addr,
		"headers": map[string]string{"Authorization": "Bearer ${AGENTGO_TEST_MCP_TOKEN}"},
	})
	if !res.Success || res.Transport != "http" || len(res.Tools) != 1 || res.Tools[0] != "svc_echo" {
		t.Fatalf("register failed: %+v", res)
	}
	if !spy.sawHeader("Authorization", "Bearer manager-secret") {
		t.Fatal("header was not sent")
	}

	saved, err := os.ReadFile(filepath.Join(ws, "mcp_servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed MCPConfigFile
	if err := json.Unmarshal(saved, &parsed); err != nil {
		t.Fatal(err)
	}
	entry := parsed.MCPServers["svc"]
	if entry.URL != addr || entry.Type != "http" || entry.Command != "" {
		t.Fatalf("persisted entry %+v", entry)
	}
	if entry.Headers["Authorization"] != "Bearer ${AGENTGO_TEST_MCP_TOKEN}" {
		t.Fatalf("the reference must be stored, not the secret: %v", entry.Headers)
	}
	if strings.Contains(string(saved), "manager-secret") {
		t.Fatal("the secret value was written to the config file")
	}

	listed := manage(map[string]any{"action": "list"})
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), "manager-secret") || strings.Contains(string(raw), "Authorization") {
		t.Fatalf("list leaks header data: %s", raw)
	}
	if len(listed.Servers) != 1 || listed.Servers[0].URL == "" {
		t.Fatalf("list: %+v", listed.Servers)
	}

	if res := manage(map[string]any{"action": "remove", "name": "svc"}); !res.Success {
		t.Fatalf("remove: %+v", res)
	}
	if len(r.ListMCPServers()) != 0 {
		t.Fatal("remove left the server loaded")
	}
}
