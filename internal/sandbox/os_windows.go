//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"agentgo/internal/shellcmd"
)

// Windows confinement, and what it is not.
//
// What it does: a confined command runs at Low integrity with no privileges, so
// it can write only in the Policy's write roots (each labelled Low once, see
// os_windows_label.go) and in a private temp folder. Writes anywhere else — the
// rest of the disk, the registry's user hive, %TEMP% itself — are refused by
// Windows. The process tree is held in the usual Job object (shellcmd).
//
// What it does not do, and cannot with a user-level token:
//   - Block reading. A confined command can read every file the user can.
//   - Block the network. Low integrity does not touch sockets; only an AppContainer
//     or the firewall can, and both need administrator rights or a package
//     identity that ordinary programs cannot be launched under.
//
// So this is a guard against a command damaging things (rm -rf, overwrite, a
// stray installer), not against a command leaking things. Because of that,
// Command refuses unless the caller has accepted the network exposure
// (Policy.AllowNetwork, from AGENTGO_SANDBOX_OS_NET=1); there is no silent
// downgrade. Policy.ReadRoots is ignored for the same reason.

var (
	availOnce  sync.Once
	availValue bool
)

// Available reports whether a restricted Low-integrity token can be made here.
func Available() bool {
	availOnce.Do(func() {
		_, err := sandboxToken()
		availValue = err == nil
	})
	return availValue
}

// privateTempDir is the TEMP and TMP of confined commands: %TEMP% itself is
// Medium and so closed to them. It lives under the user's local app data, not
// under %TEMP%, because that is inside the profile wherever TEMP points, and the
// profile is where the user may label folders (a TEMP on another disk usually
// forbids it).
func privateTempDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agentgo", "sandbox-tmp")
	return dir, os.MkdirAll(dir, 0o700)
}

// Command builds a confined command line. Everything that could weaken the
// confinement is an error, never a fallback.
func Command(ctx context.Context, line string, p Policy) (*exec.Cmd, error) {
	if !p.AllowNetwork {
		return nil, fmt.Errorf("%w: Windows can confine a command's file writes but cannot block its network access; set AGENTGO_SANDBOX_OS_NET=1 to run commands with the write confinement and network access (reads are not confined either)", ErrUnsupported)
	}
	tok, err := sandboxToken()
	if err != nil {
		return nil, fmt.Errorf("sandbox: cannot create the restricted token: %w", err)
	}

	tmp, err := privateTempDir()
	if err != nil {
		return nil, fmt.Errorf("sandbox: cannot create the private temp folder: %w", err)
	}
	if err := ensureLowLabel(tmp); err != nil {
		return nil, err
	}
	for _, root := range p.WriteRoots {
		if root == "" || sameDir(root, os.TempDir()) {
			continue // the shared temp folder stays closed; tmp above replaces it
		}
		if err := ensureLowLabel(root); err != nil {
			return nil, err
		}
	}

	cmd := shellcmd.Command(ctx, line)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	cmd.Env = withTemp(cmd.Env, tmp)
	return cmd, nil
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// withTemp returns env with TEMP and TMP pointing at dir. A nil env means "the
// parent's"; it is made explicit here.
func withTemp(env []string, dir string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "TEMP") || strings.EqualFold(name, "TMP") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TEMP="+dir, "TMP="+dir)
}
