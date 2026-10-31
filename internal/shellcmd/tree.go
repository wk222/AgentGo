package shellcmd

import (
	"fmt"
	"os/exec"
)

// ProcessTree owns a command and its descendants until completion or Kill.
// Always call Wait after a successful StartTree to release operating-system resources.
// CommandContext cancellation terminates descendants as well as the root.
type ProcessTree interface {
	Wait() error
	Kill()
}

// TreeOptions tune how StartTreeWith owns a command.
type TreeOptions struct {
	// Detachable lets what the command started on purpose outlive it: an
	// application the user asked to open, a server launched with "start". By
	// default the tree ends with its root, so nothing runs on behind a finished
	// command. Cancelling the context or calling Kill still ends every process,
	// and the resource limits keep applying to the survivors.
	Detachable bool
	// BestEffort runs the command even when the operating system refuses to put
	// it under a job (before Windows 8 a process that already sits in a job
	// cannot join another). The command then runs without ownership or limits,
	// instead of failing.
	BestEffort bool
}

// StartTree starts cmd and everything it launches as one unit that Kill, a
// cancelled context and Wait's cleanup end together.
func StartTree(cmd *exec.Cmd) (ProcessTree, error) {
	return StartTreeWith(cmd, TreeOptions{})
}

// StartTreeWith is StartTree with options.
func StartTreeWith(cmd *exec.Cmd, opts TreeOptions) (ProcessTree, error) {
	if cmd == nil {
		return nil, fmt.Errorf("start process tree: nil command")
	}
	if cmd.Process != nil {
		return nil, fmt.Errorf("start process tree: command already started")
	}
	return startTree(cmd, opts)
}
