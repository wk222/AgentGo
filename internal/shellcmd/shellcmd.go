// Package shellcmd runs one command line through the platform shell, and ends it
// together with everything it started.
//
// It exists because the two obvious ways of doing this are wrong on Windows:
//
//   - exec.Command("cmd", "/C", line) passes line as an argument, and Go escapes
//     every double quote in it as \". cmd.exe does not understand that, so a
//     command such as  powershell -Command "..."  is not run; its text is echoed.
//   - cmd.Process.Kill() ends the shell only. Whatever the command started keeps
//     running with nobody left to own it.
package shellcmd

import (
	"context"
	"os"
	"os/exec"
)

// Command returns a command that runs line through the platform shell (cmd.exe
// /S /C on Windows, sh -c elsewhere) with UTF-8 runtime environment defaults
// (PYTHONUTF8=1, PYTHONIOENCODING=utf-8). The caller sets Dir, Env and the streams
// and starts it with StartTree when cancellation must include descendants.
func Command(ctx context.Context, line string) *exec.Cmd {
	cmd := command(ctx, line)
	cmd.Env = WithUTF8Env(nil)
	return cmd
}

// WithUTF8Env returns base (or os.Environ() when base is nil) with UTF-8 defaults
// appended unless the caller already set them.
func WithUTF8Env(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, len(base), len(base)+3)
	copy(out, base)
	hasKey := func(k string) bool {
		prefix := k + "="
		for _, entry := range out {
			if len(entry) >= len(prefix) && stringsEqualFoldASCII(entry[:len(prefix)], prefix) {
				return true
			}
		}
		return false
	}
	if !hasKey("PYTHONUTF8") {
		out = append(out, "PYTHONUTF8=1")
	}
	if !hasKey("PYTHONIOENCODING") {
		out = append(out, "PYTHONIOENCODING=utf-8")
	}
	if !hasKey("LC_ALL") && !hasKey("LANG") {
		out = append(out, "LANG=en_US.UTF-8")
	}
	return out
}

func stringsEqualFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
