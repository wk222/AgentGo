//go:build !windows

package shellcmd

import (
	"os/exec"
	"time"
)

type groupedProcessTree struct {
	cmd        *exec.Cmd
	detachable bool
}

func startTree(cmd *exec.Cmd, opts TreeOptions) (ProcessTree, error) {
	prepare(cmd)
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { killTree(cmd); return nil }
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 2 * time.Second
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &groupedProcessTree{cmd: cmd, detachable: opts.Detachable}, nil
}

func (t *groupedProcessTree) Wait() error {
	err := t.cmd.Wait()
	if !t.detachable {
		killTree(t.cmd)
	}
	return err
}

func (t *groupedProcessTree) Kill() { killTree(t.cmd) }
