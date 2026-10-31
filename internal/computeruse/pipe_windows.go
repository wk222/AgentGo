//go:build windows

package computeruse

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// userKey is the current user's SID string. It names the pipe and token file
// and anchors the pipe's DACL; an elevated and a non-elevated process of the
// same user share it, so an elevated sidecar stays reachable from the normal
// desktop app (the whole point of running one).
func userKey() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "unknown"
	}
	return u.User.Sid.String()
}

// PipeName is the per-user named pipe the sidecar listens on.
func PipeName() string { return `\\.\pipe\agentgo-cua-` + safeName(userKey()) }

// pipeSDDL grants full access to the current user and SYSTEM only. "P" marks
// the DACL protected so nothing is inherited from a default; with no entry for
// Everyone or Network, other users and remote callers cannot even open it.
func pipeSDDL() string { return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", userKey()) }

// Listen creates the sidecar pipe through go-winio. ListenPipe opens the first
// instance with FILE_FLAG_FIRST_PIPE_INSTANCE (a second sidecar - or a squatter
// that got there first - fails instead of silently sharing the name) and
// rejects remote clients.
func Listen(name string) (net.Listener, error) {
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL(),
		InputBufferSize:    1 << 20,
		OutputBufferSize:   4 << 20, // PNG replies
	})
}

// DialPipe connects to a sidecar pipe, retrying until ctx expires while the
// sidecar is still starting.
func DialPipe(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, name)
}
