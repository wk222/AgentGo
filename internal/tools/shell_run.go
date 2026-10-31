package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/sandbox"
	"agentgo/internal/shellcmd"
)

const (
	// shellOutputCap bounds the output of one command that is held in memory,
	// per stream. A loop that prints forever must not take the agent down; the
	// start and the end are kept and the gap is counted.
	shellOutputCap = 8 << 20
	// maxShellTimeout is the longest a single command may be given.
	maxShellTimeout = 30 * time.Minute
	// shellTimeoutStatus is the exit status reported for a command that ran out
	// of time, the one coreutils' timeout uses.
	shellTimeoutStatus = 124
)

// shellTimeout is the time limit of one command: what the model asked for in
// seconds (0 = nothing), else the configured default. 0 means no limit.
func shellTimeout(requestedSeconds int) time.Duration {
	secs := requestedSeconds
	if secs <= 0 {
		secs = config.ShellTimeoutSec.Get()
	}
	if secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, maxShellTimeout)
}

// shellTimeoutNote tells the model how long a command may run.
func shellTimeoutNote() string {
	longest := int(maxShellTimeout / time.Second)
	def := shellTimeout(0)
	if def == 0 {
		return fmt.Sprintf("Without timeout_seconds a command has no time limit; timeout_seconds sets one (at most %d).", longest)
	}
	return fmt.Sprintf("A command is stopped after %d seconds unless timeout_seconds asks for longer (at most %d); stopping it ends everything it started.",
		int(def/time.Second), longest)
}

// runShell runs one command line in dir. The command and everything it starts
// form one process tree: when the time is up or ctx ends, all of it is ended,
// not only the shell. A tree has the memory and process limits of shellcmd, and
// what the command leaves running on purpose (an application the user asked to
// open) is left alone once it finishes normally.
func runShell(ctx context.Context, dir, line string, timeout time.Duration) (bashOutput, error) {
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := shellcmd.Command(runCtx, line)
	if config.SandboxOS.On() {
		// 明确要求 OS 沙箱：不支持就拒绝执行，不悄悄无约束运行。
		confined, err := sandbox.Confine(runCtx, line, dir,
			sandbox.SplitRoots(config.SandboxOSReadRoots.Get()), config.SandboxOSNet.On())
		if err != nil {
			return bashOutput{}, fmt.Errorf("AGENTGO_SANDBOX_OS is on but the command cannot be confined, so it was not run: %w (to run without confinement, unset AGENTGO_SANDBOX_OS)", err)
		}
		cmd = confined
	}
	cmd.Dir = dir
	stdout, stderr := shellcmd.NewCapture(shellOutputCap), shellcmd.NewCapture(shellOutputCap)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{Detachable: true, BestEffort: true})
	if ctx.Err() != nil {
		return bashOutput{}, ctx.Err() // the run was cancelled from outside
	}
	timedOut := runCtx.Err() != nil && (err != nil || code != 0)
	if err != nil && !timedOut {
		return bashOutput{}, err
	}

	out := bashOutput{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
	if timedOut {
		out.ExitCode = shellTimeoutStatus
		out.Stderr = strings.TrimRight(out.Stderr, "\r\n") + fmt.Sprintf(
			"\n[timeout] the command ran longer than %d seconds and was stopped together with everything it started. "+
				"If it really needs more time, run it again with a larger timeout_seconds.", int(timeout/time.Second))
		return out, nil
	}
	if hint := elevationHint(out.Stdout, out.Stderr, code); hint != "" {
		out.Stderr = strings.TrimRight(out.Stderr, "\r\n") + "\n" + hint
	}
	return out, nil
}
