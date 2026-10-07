// Package shellcmd runs one command line through the platform shell, and ends it
// together with everything it started. Standard library only.
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
	"os/exec"
)

// Command returns a command that runs line through the platform shell (cmd.exe
// /S /C on Windows, sh -c elsewhere). The caller sets Dir, Env and the streams
// and starts it. If the command is to be ended with KillTree, call Prepare first.
func Command(ctx context.Context, line string) *exec.Cmd {
	return command(ctx, line)
}

// Prepare arranges for the command's whole process tree to be endable later with
// KillTree. Call it before Start.
func Prepare(cmd *exec.Cmd) {
	prepare(cmd)
}

// KillTree ends cmd and everything it started. Safe to call on a command that
// never started or has already exited.
func KillTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killTree(cmd)
}
