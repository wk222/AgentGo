package userhooks

import (
	"bytes"
	"context"
	"os"
	"time"

	"agentgo/internal/shellcmd"
)

// scriptRun is what happened when one hook command ran.
type scriptRun struct {
	Code     int
	Stdout   string
	Stderr   string
	Elapsed  time.Duration
	TimedOut bool  // ran out of its time and was ended with everything it started
	Canceled bool  // the run it belonged to was cancelled
	Err      error // could not be started or waited for
}

// runScript runs the hook's command line through the platform shell, with stdin
// as its standard input, in dir when that exists. The command and everything it
// starts form one process tree under the same memory and process limits as any
// other command; when the time is up or ctx ends, all of it is ended, and
// nothing it left running outlives it. At most OutputCap bytes of each output
// stream are kept.
func runScript(ctx context.Context, h Hook, dir string, env []string, stdin []byte) scriptRun {
	runCtx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()

	cmd := shellcmd.Command(runCtx, h.Command)
	if st, err := os.Stat(dir); dir != "" && err == nil && st.IsDir() {
		cmd.Dir = dir
	}
	cmd.Env = append(cmd.Env, env...) // the last value of a repeated name wins
	cmd.Stdin = bytes.NewReader(stdin)
	stdout, stderr := shellcmd.NewCapture(OutputCap), shellcmd.NewCapture(OutputCap)
	cmd.Stdout, cmd.Stderr = stdout, stderr

	start := time.Now()
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{BestEffort: true})
	res := scriptRun{Code: code, Stdout: decodeOutput(stdout.String()), Stderr: decodeOutput(stderr.String()), Elapsed: time.Since(start)}
	switch {
	case ctx.Err() != nil:
		res.Canceled = true
	case runCtx.Err() != nil && (err != nil || code != 0):
		res.TimedOut = true
	case err != nil:
		res.Err = err
	}
	return res
}
