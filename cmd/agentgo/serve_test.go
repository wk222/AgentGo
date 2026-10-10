package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPrepareServeEnvDefaultsToLoopbackWithToken(t *testing.T) {
	t.Setenv("AGENTGO_GATEWAY_ADDR", "")
	t.Setenv("AGENTGO_GATEWAY_PORT", "")
	t.Setenv("AGENTGO_GATEWAY_TOKEN", "")
	addr, token := prepareServeEnv()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("serve must default to loopback, got %q", addr)
	}
	if len(token) < 32 || os.Getenv("AGENTGO_GATEWAY_TOKEN") != token {
		t.Fatalf("expected generated token exported to env, got %q", token)
	}
}

func TestPrepareServeEnvKeepsUserToken(t *testing.T) {
	t.Setenv("AGENTGO_GATEWAY_ADDR", "")
	t.Setenv("AGENTGO_GATEWAY_PORT", "9001")
	t.Setenv("AGENTGO_GATEWAY_TOKEN", "mine")
	addr, token := prepareServeEnv()
	if addr != "127.0.0.1:9001" || token != "mine" {
		t.Fatalf("got %q %q", addr, token)
	}
}

func TestExistingServeIgnoresStaleFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeServeInfo(dir, serveInfo{Addr: "127.0.0.1:1", Token: "x", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := existingServe(dir); ok {
		t.Fatal("stale serve.json (nothing listening) must not count as a running instance")
	}
}

func TestExistingServeDetectsLiveInstance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	dir := t.TempDir()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := writeServeInfo(dir, serveInfo{Addr: addr, Token: "x", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if info, ok := existingServe(dir); !ok || info.Addr != addr {
		t.Fatalf("live instance not detected: %+v %v", info, ok)
	}
}
