package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

	"agentgo/internal/shellcmd"
)

// BackgroundTaskCoordinator manages durable background tasks, recoverable tools,
// and process execution for AgentGo.
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

// NewBackgroundTaskCoordinator creates and initializes a complete durable background task coordinator.
func NewBackgroundTaskCoordinator(workspaceRoot, dataDir string) (*BackgroundTaskCoordinator, error) {
	store := backgroundtask.NewInMemoryStore(nil)
	executors := backgroundtask.NewExecutorRegistry()
	toolReg := backgroundtool.NewRegistry()

	ctx := context.Background()
	mgr, err := backgroundtask.New(ctx, &backgroundtask.Config{
		Tasks: store,
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
	if req == nil || req.TaskID == "" {
		return nil, errors.New("os_shell: task_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errors.New("os_shell: shut down, not starting new commands")
	}
	if existing, ok := s.runs[req.TaskID]; ok && !existing.isTerminal() {
		return existing, nil
	}

	run := newOSCommandRun(req.TaskID, req.Command, s.workspaceRoot)
	s.runs[req.TaskID] = run
	run.start()

	return run, nil
}

func (s *OSRecoverableShell) RecoverCommand(ctx context.Context, req *backgroundshell.RecoverCommandRequest) (backgroundtool.Run, error) {
	if req == nil || req.TaskID == "" {
		return nil, errors.New("os_shell: task_id is required")
	}

	s.mu.RLock()
	run, ok := s.runs[req.TaskID]
	s.mu.RUnlock()

	if ok {
		return run, nil
	}

	// Re-attach or create a reconstituted run if lost
	return s.StartCommand(ctx, &backgroundshell.StartCommandRequest{
		TaskID:  req.TaskID,
		Command: req.Command,
		Attempt: req.Attempt,
	})
}

type osCommandRun struct {
	taskID        string
	command       string
	workspaceRoot string

	cmd      *exec.Cmd
	cancelFn context.CancelFunc

	doneCh chan struct{}
	pipeR  *schema.StreamReader[*backgroundtool.Update]
	pipeW  *schema.StreamWriter[*backgroundtool.Update]

	mu       sync.Mutex
	output   bytes.Buffer
	exitCode *int
	execErr  error
	status   backgroundtask.Status
	updateSeq int64
	terminal  int32

	stopped    bool   // halt was called; the final status is canceled, whatever the exit code
	stopReason string // recorded as the error when set
}

func newOSCommandRun(taskID, command, workspaceRoot string) *osCommandRun {
	sr, sw := schema.Pipe[*backgroundtool.Update](64)
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
	shellcmd.Prepare(cmd)
	r.cmd = cmd

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		r.finishWith(nil, err, backgroundtask.StatusFailed)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		r.finishWith(nil, err, backgroundtask.StatusFailed)
		return
	}

	if err := cmd.Start(); err != nil {
		r.finishWith(nil, err, backgroundtask.StatusFailed)
		return
	}

	// finishWith closes the update stream, so no pump may still be sending when
	// it runs. cmd.Wait closes the pipes, which makes a pump's pending Read fail
	// and the pump return, so waiting for the pumps cannot hang.
	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() { defer pumps.Done(); r.pumpStream("stdout", stdoutPipe) }()
	go func() { defer pumps.Done(); r.pumpStream("stderr", stderrPipe) }()

	go func() {
		waitErr := cmd.Wait()
		pumps.Wait()
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
		r.mu.Lock()
		stopped, reason := r.stopped, r.stopReason
		r.mu.Unlock()
		if stopped { // killed on purpose: say so, not "exit status 1"
			status = backgroundtask.StatusCanceled
			if reason != "" {
				waitErr = errors.New(reason)
			}
		}
		r.finishWith(&code, waitErr, status)
	}()
}

func (r *osCommandRun) pumpStream(kind string, reader io.Reader) {
	buf := make([]byte, 2048)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			r.mu.Lock()
			r.output.Write(chunk)
			seq := atomic.AddInt64(&r.updateSeq, 1)
			r.mu.Unlock()

			r.pipeW.Send(&backgroundtool.Update{
				EventID: fmt.Sprintf("%s-%d", r.taskID, seq),
				Kind:    kind,
				Data:    chunk,
			}, nil)
		}
		if err != nil {
			break
		}
	}
}

func (r *osCommandRun) finishWith(code *int, err error, status backgroundtask.Status) {
	r.mu.Lock()
	r.exitCode = code
	r.execErr = err
	r.status = status
	r.mu.Unlock()

	atomic.StoreInt32(&r.terminal, 1)
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

		outBytes := r.output.Bytes()
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

// halt ends the command and everything it started. The tree is killed before
// the context is cancelled: cancelling kills only the shell, and once the shell
// is gone its children can no longer be found.
func (r *osCommandRun) halt(reason string) {
	if r.isTerminal() {
		return
	}
	r.mu.Lock()
	r.stopped = true
	if reason != "" && r.stopReason == "" {
		r.stopReason = reason
	}
	r.mu.Unlock()
	shellcmd.KillTree(r.cmd)
	if r.cancelFn != nil {
		r.cancelFn()
	}
}

func (r *osCommandRun) Updates() *schema.StreamReader[*backgroundtool.Update] {
	return r.pipeR
}
