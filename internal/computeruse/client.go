package computeruse

import (
	"context"
	"os/exec"
	"time"

	"agentgo/internal/processutil"
)

// Connect attaches to an already running sidecar.
func Connect(ctx context.Context) (*Client, error) {
	token, err := ReadToken()
	if err != nil {
		return nil, NewError(CodeSidecarUnavailable, "%v", err)
	}
	conn, err := DialPipe(ctx, PipeName())
	if err != nil {
		return nil, NewError(CodeSidecarUnavailable, "could not reach the computer-use sidecar pipe: %v", err)
	}
	return NewClient(conn, token, 0)
}

// LaunchSidecar writes the launch token, starts exe and connects to it. The
// token must exist before the sidecar starts: it refuses to listen without one.
// The returned Cmd is already being waited on; Kill it to stop the sidecar.
func LaunchSidecar(ctx context.Context, exe string, args ...string) (*Client, *exec.Cmd, error) {
	if _, err := EnsureToken(); err != nil {
		return nil, nil, NewError(CodeSidecarUnavailable, "writing the launch token: %v", err)
	}
	cmd := exec.Command(exe, append([]string{"--pipe", PipeName()}, args...)...)
	processutil.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, NewError(CodeSidecarUnavailable, "starting %s: %v", exe, err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	deadline := time.Now().Add(8 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return nil, nil, NewError(CodeSidecarUnavailable, "the sidecar exited during startup (%v)", last)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, nil, NewError(CodeTimeout, "cancelled while starting the sidecar")
		default:
		}
		dctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		c, err := Connect(dctx)
		cancel()
		if err == nil {
			return c, cmd, nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return nil, nil, NewError(CodeSidecarUnavailable, "the sidecar did not accept a connection in time (%v)", last)
}
