package acpclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"agentgo/internal/shellcmd"
)

// stderrKeep is how much of the agent's log is kept to explain a failure.
const stderrKeep = 8 << 10

// startProcess runs cfg.Command through the platform shell (so "npx …" resolves npx.cmd on
// Windows) with the agent's stdio wired to us. The process is not tied to ctx: a session may
// outlive the call that opened it, and Close ends it together with everything it started.
func startProcess(cfg Config) (transport, error) {
	line := strings.TrimSpace(cfg.Command)
	if line == "" {
		return transport{}, errors.New("no command to start the agent")
	}
	if cfg.Cwd != "" {
		if st, err := os.Stat(cfg.Cwd); err != nil || !st.IsDir() {
			return transport{}, fmt.Errorf("working directory %q does not exist", cfg.Cwd)
		}
	}
	cmd := shellcmd.Command(context.Background(), line)
	cmd.Dir = cfg.Cwd
	cmd.Env = shellcmd.WithUTF8Env(append(os.Environ(), cfg.Env...))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return transport{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return transport{}, err
	}
	logs := shellcmd.NewCapture(stderrKeep)
	cmd.Stderr = logs
	tree, err := shellcmd.StartTree(cmd)
	if err != nil {
		return transport{}, fmt.Errorf("start %q: %w", line, err)
	}
	reaped := make(chan struct{})
	go func() {
		_ = tree.Wait()
		close(reaped)
	}()
	var once sync.Once
	return transport{
		in:     stdout,
		out:    stdin,
		stderr: logs.String,
		close: func() {
			once.Do(func() {
				_ = stdin.Close()
				tree.Kill()
			})
		},
		wait: func() { <-reaped },
	}, nil
}
