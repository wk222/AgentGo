//go:build windows

package computeruse

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSidecarProcessE2E starts the real agentgo-cua.exe as a child process and
// drives it over the real pipe. Read-only (screenshot + zoom), but it does
// capture the screen, so it only runs when AGENTGO_CUA_SIDECAR_EXE is set:
//
//	go build -o <dir>\agentgo-cua.exe ./cmd/agentgo-cua
//	$env:AGENTGO_CUA_SIDECAR_EXE='<dir>\agentgo-cua.exe'; go test -run Sidecar ./internal/computeruse
func TestSidecarProcessE2E(t *testing.T) {
	exe := os.Getenv("AGENTGO_CUA_SIDECAR_EXE")
	if exe == "" {
		t.Skip("set AGENTGO_CUA_SIDECAR_EXE to the built agentgo-cua.exe")
	}
	// Child inherits this: token file and pipe name are isolated from any real run.
	t.Setenv("LocalAppData", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, cmd, err := LaunchSidecar(ctx, exe, "--no-hotkey", "--idle", "1m")
	if err != nil {
		t.Fatalf("LaunchSidecar: %v", err)
	}
	defer func() { _ = c.Close(); _ = cmd.Process.Kill() }()

	res, err := c.Do(ctx, Request{Action: "screenshot"})
	if err != nil {
		t.Fatalf("screenshot through the sidecar: %v", err)
	}
	w, h := pngSize(t, res.PNG)
	t.Logf("sidecar pid %d -> %s (%dx%d, %d KB)", cmd.Process.Pid, res.Text, w, h, len(res.PNG)/1024)

	// Actions that need a prior frame work in the child's own session state.
	if _, err = c.Do(ctx, Request{Action: "click", X: fp(float64(w) + 50), Y: fp(5)}); AsError(err).Code != CodeInvalidArguments {
		t.Fatalf("out-of-bounds click should be rejected by the sidecar: %v", err)
	}

	// A second client with the wrong token must be refused by the live process.
	conn, err := DialPipe(ctx, PipeName())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewClient(conn, "00"+testToken[2:], time.Second); err == nil {
		t.Fatal("the sidecar accepted a wrong token")
	}
}
