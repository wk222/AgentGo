package crushproto

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Acceptance against the REAL Crush client: `crush run` talks to this server
// over TCP and must print the scripted answer. Run after every Crush upgrade:
//
//	$env:AGENTGO_CRUSHPROTO_E2E = "1"
//	$env:AGENTGO_CRUSH_EXE = "C:\...\crush\crush.exe"   # default: ..\..\..\crush\crush.exe
//	go test -run TestE2ERealCrushClient -v ./internal/crushproto/
func TestE2ERealCrushClient(t *testing.T) {
	if os.Getenv("AGENTGO_CRUSHPROTO_E2E") != "1" {
		t.Skip("set AGENTGO_CRUSHPROTO_E2E=1 to run")
	}
	exe := os.Getenv("AGENTGO_CRUSH_EXE")
	if exe == "" {
		exe = filepath.Join("..", "..", "..", "crush", "crush.exe")
	}
	exe, _ = filepath.Abs(exe) // cmd.Dir changes below: a relative path would stop resolving
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("crush executable not found: %s", exe)
	}

	srv, err := New(Options{
		SkipPermissions: true,
		Runner:          ScriptRunner{Pace: time.Millisecond},
		Logf:            t.Logf, // safe for concurrent use
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "run", "-H", "tcp://"+strings.TrimPrefix(ts.URL, "http://"),
		"-D", t.TempDir(), "make hello")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "CRUSH_CLIENT_SERVER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "AgentGo protocol probe") {
		t.Fatalf("client did not print the scripted answer:\n%s", out)
	}
}
