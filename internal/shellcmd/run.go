package shellcmd

import (
	"errors"
	"os/exec"
)

// Run starts cmd with StartTreeWith and waits for it. It returns the exit
// status; a command that ends with a non-zero status is not an error. An error
// means the command could not be started or waited for.
//
// A command ended by its context (see Command) reports a non-zero status, so a
// caller that sets a deadline checks the context to tell "failed" from "stopped".
func Run(cmd *exec.Cmd, opts TreeOptions) (int, error) {
	tree, err := StartTreeWith(cmd, opts)
	if err != nil {
		return -1, err
	}
	return exitStatus(cmd, tree.Wait())
}

// exitStatus maps what Wait returned to the command's exit status.
func exitStatus(cmd *exec.Cmd, err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	// The command exited but something it left running still held its output
	// pipe open past WaitDelay. The command itself finished; its output so far is complete.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return -1, err
}
