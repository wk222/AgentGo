package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundlocal "github.com/cloudwego/eino/adk/backgroundtask/local"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
	backgroundtool "github.com/cloudwego/eino/adk/backgroundtask/tool"
	backgroundtaskmw "github.com/cloudwego/eino/adk/middlewares/backgroundtask"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/processutil"
	"agentgo/internal/shellcmd"
)

// BackgroundTaskCoordinator manages runtime background tasks and local processes.
// Local task records are process-local; an unknown execution is never replayed.
type BackgroundTaskCoordinator struct {
	manager         *backgroundtask.Manager
	store           *backgroundtask.InMemoryStore
	executors       *backgroundtask.ExecutorRegistry
	toolRegistry    *backgroundtool.Registry
	localRunner     *backgroundlocal.Runner
	shell           backgroundshell.RecoverableShell
	osShell         *OSRecoverableShell
	progressReaders map[string]backgroundtaskmw.TaskProgressReader
	workspaceRoot   string
	dataDir         string
	closeOnce       sync.Once
}

// ShutdownReason is the error recorded on commands that were still running when
// AgentGo exited. Task state lives in memory only, so nothing could pick such a
// command up again: it is ended rather than left running with no owner.
const ShutdownReason = "interrupted: AgentGo exited while the command was running"

// Close ends every command still running (the whole process tree, not just the
// shell), then closes the task manager. Idempotent. It returns after a bounded
// wait and never blocks shutdown on a command that will not die.
func (c *BackgroundTaskCoordinator) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		if c.osShell != nil {
			c.osShell.Close(3 * time.Second)
		}
		if c.manager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = c.manager.Close(ctx)
		}
	})
	return err
}

// NewBackgroundTaskCoordinator initializes the runtime task coordinator.
func NewBackgroundTaskCoordinator(workspaceRoot, dataDir string) (*BackgroundTaskCoordinator, error) {
	store := backgroundtask.NewInMemoryStore(nil)
	executors := backgroundtask.NewExecutorRegistry()
	toolReg := backgroundtool.NewRegistry()

	ctx := context.Background()
	// The manager resolves a task's executor through this registry; without it
	// every task fails with "executor ... is unavailable".
	mgr, err := backgroundtask.New(ctx, &backgroundtask.Config{
		Tasks:     store,
		Executors: executors,
	})
	if err != nil {
		return nil, fmt.Errorf("init backgroundtask manager failed: %w", err)
	}

	if err := backgroundtool.RegisterExecutors(executors, toolReg); err != nil {
		return nil, fmt.Errorf("register backgroundtool executors failed: %w", err)
	}

	localRun, err := backgroundlocal.New(&backgroundlocal.Config{
		Manager:   mgr,
		Executors: executors,
	})
	if err != nil {
		return nil, fmt.Errorf("init backgroundlocal runner failed: %w", err)
	}

	recShell := NewOSRecoverableShell(workspaceRoot)
	shellReg, err := backgroundshell.NewRegistration(&backgroundshell.RegistrationConfig{
		Info: &schema.ToolInfo{
			Name: "bash",
			Desc: "Execute shell command in background or foreground with recovery capability",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"command": {
					Type:     schema.String,
					Desc:     "The shell command to execute",
					Required: true,
				},
			}),
		},
		Shell: recShell,
	})
	if err == nil && shellReg != nil {
		_ = toolReg.Register(shellReg)
	}

	progressReaders := make(map[string]backgroundtaskmw.TaskProgressReader)
	if reader, err := backgroundtool.NewProgressReader(mgr, 30); err == nil && reader != nil {
		progressReaders[backgroundtool.ExecutorKey] = reader
		progressReaders[backgroundtool.RecoverableExecutorKey] = reader
	}

	return &BackgroundTaskCoordinator{
		manager:         mgr,
		store:           store,
		executors:       executors,
		toolRegistry:    toolReg,
		localRunner:     localRun,
		shell:           recShell,
		osShell:         recShell,
		progressReaders: progressReaders,
		workspaceRoot:   workspaceRoot,
		dataDir:         dataDir,
	}, nil
}

// StartBash runs work as a background task and returns its task ID at once. It
// is how execute_bash honours run_in_background: the model gets the ID, runs
// other things meanwhile, and collects the result with task_output. The task
// outlives the calling turn but not the process (see Close).
func (c *BackgroundTaskCoordinator) StartBash(ctx context.Context, command string, work func(context.Context) (string, error)) (string, error) {
	if c == nil || c.localRunner == nil {
		return "", errors.New("background tasks are not available")
	}
	payload, err := json.Marshal(map[string]any{"version": 1, "command": command})
	if err != nil {
		return "", err
	}
	task, err := c.localRunner.Run(ctx, &backgroundlocal.Input{
		Description:     command,
		Kind:            "bash",
		Payload:         payload,
		RunInBackground: true,
		// No parent session: the manager only accepts one when it has a
		// session-event sender, and nothing here waits for a notification; the
		// model collects the result with task_output.
	}, func(workCtx context.Context, _ backgroundtask.ExecutionRuntime) (string, error) {
		return work(workCtx)
	})
	if err != nil {
		return "", err
	}
	return task.Spec.ID, nil
}

// Manager returns the active background task manager.
func (c *BackgroundTaskCoordinator) Manager() *backgroundtask.Manager {
	if c == nil {
		return nil
	}
	return c.manager
}

// Store returns the underlying in-memory store.
func (c *BackgroundTaskCoordinator) Store() *backgroundtask.InMemoryStore {
	if c == nil {
		return nil
	}
	return c.store
}

// Executors returns the executor registry.
func (c *BackgroundTaskCoordinator) Executors() *backgroundtask.ExecutorRegistry {
	if c == nil {
		return nil
	}
	return c.executors
}

// ProgressReaders returns the map of task progress readers by executor key.
func (c *BackgroundTaskCoordinator) ProgressReaders() map[string]backgroundtaskmw.TaskProgressReader {
	if c == nil {
		return nil
	}
	return c.progressReaders
}

// LocalRunner returns the local background runner.
func (c *BackgroundTaskCoordinator) LocalRunner() *backgroundlocal.Runner {
	if c == nil {
		return nil
	}
	return c.localRunner
}

// Shell returns the recoverable shell.
func (c *BackgroundTaskCoordinator) Shell() backgroundshell.RecoverableShell {
	if c == nil {
		return nil
	}
	return c.shell
}

// OSRecoverableShell implements backgroundshell.RecoverableShell for local OS command executions.
type OSRecoverableShell struct {
	workspaceRoot string
	mu            sync.RWMutex
	runs          map[string]*osCommandRun
	closed        bool
}

// Close stops every run that has not finished and waits up to wait for them to
// report it. Afterwards no new command can be started. Returns how many it had
// to stop.
func (s *OSRecoverableShell) Close(wait time.Duration) int {
	s.mu.Lock()
	s.closed = true
	var live []*osCommandRun
	for _, r := range s.runs {
		if !r.isTerminal() {
			live = append(live, r)
		}
	}
	s.mu.Unlock()

	for _, r := range live {
		r.halt(ShutdownReason)
	}
	deadline := time.After(wait)
	for _, r := range live {
		select {
		case <-r.doneCh:
		case <-deadline:
			return len(live)
		}
	}
	return len(live)
}

// NewOSRecoverableShell constructs an OSRecoverableShell bound to the workspace directory.
func NewOSRecoverableShell(workspaceRoot string) *OSRecoverableShell {
	return &OSRecoverableShell{
		workspaceRoot: filepath.Clean(workspaceRoot),
		runs:          make(map[string]*osCommandRun),
	}
}

func (s *OSRecoverableShell) StartCommand(ctx context.Context, req *backgroundshell.StartCommandRequest) (backgroundtool.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil || req.TaskID == "" {
		return nil, errors.New("os_shell: task_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errors.New("os_shell: shut down, not starting new commands")
	}
	if existing, ok := s.runs[req.TaskID]; ok {
		if existing.command != req.Command {
			return nil, errors.New("os_shell: task_id already belongs to a different command")
		}
		// Failed and cancelled commands can also have external side effects.
		// The same execution ID is idempotent for every terminal outcome;
		// intentional retry requires an explicit new task ID.
		return existing, nil
	}

	run := newOSCommandRun(req.TaskID, req.Command, s.workspaceRoot)
	s.runs[req.TaskID] = run
	run.start()

	return run, nil
}

func (s *OSRecoverableShell) RecoverCommand(ctx context.Context, req *backgroundshell.RecoverCommandRequest) (backgroundtool.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil || req.TaskID == "" {
		return nil, errors.New("os_shell: task_id is required")
	}

	s.mu.RLock()
	run, ok := s.runs[req.TaskID]
	s.mu.RUnlock()

	if ok {
		if req.Command != "" && req.Command != run.command {
			return nil, errors.New("os_shell: recovery command does not match the original execution")
		}
		return run, nil
	}

	// Process/task state is not persisted. Re-executing an unknown command could
	// repeat external side effects after a restart, so recovery must fail closed.
	return nil, fmt.Errorf("os_shell: cannot recover task %q: prior execution and side effects are unknown; verify actual state before explicitly retrying", req.TaskID)
}

type osCommandRun struct {
	taskID        string
	command       string
	workspaceRoot string

	cmd      *exec.Cmd
	tree     shellcmd.ProcessTree
	cancelFn context.CancelFunc

	doneCh chan struct{}
	pipeR  *schema.StreamReader[*backgroundtool.Update]
	pipeW  *schema.StreamWriter[*backgroundtool.Update]

	mu        sync.Mutex
	output    processutil.OutputBuffer
	exitCode  *int
	execErr   error
	status    backgroundtask.Status
	updateSeq int64
	terminal  int32

	stopped    bool   // halt was called; the final status is canceled, whatever the exit code
	stopReason string // recorded as the error when set
}

// Preview updates are bounded even when no caller drains Updates(). The final
// Wait snapshot remains independently available, capped at 1 MiB.
const maxCommandPreviewUpdates = 512

func newOSCommandRun(taskID, command, workspaceRoot string) *osCommandRun {
	sr, sw := schema.Pipe[*backgroundtool.Update](maxCommandPreviewUpdates)
	return &osCommandRun{
		taskID:        taskID,
		command:       command,
		workspaceRoot: workspaceRoot,
		doneCh:        make(chan struct{}),
		pipeR:         sr,
		pipeW:         sw,
		status:        backgroundtask.StatusRunning,
	}
}

func (r *osCommandRun) isTerminal() bool {
	return atomic.LoadInt32(&r.terminal) == 1
}

func (r *osCommandRun) start() {
	cmdCtx, cancel := context.WithCancel(context.Background())
	r.cancelFn = cancel

	cmd := shellcmd.Command(cmdCtx, r.command)
	if r.workspaceRoot != "" {
		cmd.Dir = r.workspaceRoot
	}
	r.cmd = cmd

	// Let exec drain stdout/stderr before Wait returns. With StdoutPipe followed
	// by Wait, a quick command could have its unread output pipes closed early.
	cmd.Stdout = &commandOutputWriter{run: r, kind: "stdout"}
	cmd.Stderr = &commandOutputWriter{run: r, kind: "stderr"}
	cmd.WaitDelay = 2 * time.Second
	tree, err := shellcmd.StartTree(cmd)
	if err != nil {
		r.finishWith(nil, err, backgroundtask.StatusFailed)
		return
	}

	r.tree = tree
	go func() {
		waitErr := tree.Wait()
		code := 0
		if waitErr != nil {
			if ee, ok := waitErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = 1
			}
		}
		status := backgroundtask.StatusCompleted
		if code != 0 {
			status = backgroundtask.StatusFailed
		}
		r.finishWith(&code, waitErr, status)
	}()
}

type commandOutputWriter struct {
	run  *osCommandRun
	kind string
}

func (w *commandOutputWriter) Write(data []byte) (int, error) {
	r := w.run
	r.mu.Lock()
	defer r.mu.Unlock()
	r.output.Write(data)
	// Progress is a bounded preview, independent from the final output snapshot.
	// At most 512 chunks of 2 KiB can queue, so Wait/Close work without a reader.
	for offset := 0; offset < len(data); offset += 2048 {
		seq := atomic.AddInt64(&r.updateSeq, 1)
		if seq > maxCommandPreviewUpdates {
			continue
		}
		chunk := append([]byte(nil), data[offset:min(offset+2048, len(data))]...)
		r.pipeW.Send(&backgroundtool.Update{EventID: fmt.Sprintf("%s-%d", r.taskID, seq), Kind: w.kind, Data: chunk}, nil)
	}
	return len(data), nil
}

func (r *osCommandRun) finishWith(code *int, err error, status backgroundtask.Status) {
	r.mu.Lock()
	// Publish the stop decision and final outcome under the same lock. Otherwise
	// cancellation racing with normal exit could be overwritten by "completed".
	if r.stopped {
		status = backgroundtask.StatusCanceled
		if r.stopReason != "" {
			err = errors.New(r.stopReason)
		}
	}
	r.exitCode = code
	r.execErr = err
	r.status = status
	atomic.StoreInt32(&r.terminal, 1)
	r.mu.Unlock()
	if r.cancelFn != nil {
		r.cancelFn()
	}

	r.pipeW.Close()
	close(r.doneCh)
}

func (r *osCommandRun) Wait(ctx context.Context) (*backgroundtool.Outcome, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.doneCh:
		r.mu.Lock()
		defer r.mu.Unlock()

		outBytes := []byte(r.output.String())
		errStr := ""
		if r.execErr != nil {
			errStr = r.execErr.Error()
		}

		return &backgroundtool.Outcome{
			Status: r.status,
			Data:   outBytes,
			Error:  errStr,
		}, nil
	}
}

func (r *osCommandRun) Stop(ctx context.Context) error {
	r.halt("")
	return nil
}

// halt ends the owned process tree and marks its eventual outcome as cancelled.
func (r *osCommandRun) halt(reason string) {
	if r.isTerminal() {
		return
	}
	r.mu.Lock()
	if r.isTerminal() {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	if reason != "" && r.stopReason == "" {
		r.stopReason = reason
	}
	r.mu.Unlock()
	if r.tree != nil {
		r.tree.Kill()
	}
	if r.cancelFn != nil {
		r.cancelFn()
	}
}

func (r *osCommandRun) Updates() *schema.StreamReader[*backgroundtool.Update] {
	return r.pipeR
}
