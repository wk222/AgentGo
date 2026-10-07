package ideruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	pty "github.com/aymanbagabas/go-pty"
)

// ProcessKind represents the purpose of the spawned process.
type ProcessKind string

const (
	KindTerminal   ProcessKind = "terminal"
	KindTask       ProcessKind = "task"
	KindSearch     ProcessKind = "search"
	KindGit        ProcessKind = "git"
	KindDiagnostic ProcessKind = "diagnostic"
)

// ProcessSpec defines the specifications for spawning a process.
type ProcessSpec struct {
	ID      string            `json:"id"`
	Kind    ProcessKind       `json:"kind"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Cwd     string            `json:"cwd"`
	Env     []string          `json:"env,omitempty"`
	UsePTY  bool              `json:"use_pty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
}

// ProcessInfo represents observable state for a managed process.
type ProcessInfo struct {
	ID        string      `json:"id"`
	Kind      ProcessKind `json:"kind"`
	PID       int         `json:"pid"`
	Command   string      `json:"command"`
	Running   bool        `json:"running"`
	ExitCode  int         `json:"exit_code"`
	StartTime time.Time   `json:"start_time"`
}

// ProcessHandle provides control over an actively managed process.
type ProcessHandle interface {
	ID() string
	Kind() ProcessKind
	PID() int
	Write(data []byte) error
	Resize(cols, rows int) error
	Interrupt() error
	Kill() error
	Wait() error
	ExitCode() int
	IsRunning() bool
	OnOutput(cb func(data []byte))
	OnExit(cb func(exitCode int))
}

// ProcessSupervisor coordinates sub-processes, preventing orphan/zombie processes
// via Windows Job Objects (and process groups on Unix).
type ProcessSupervisor struct {
	mu        sync.RWMutex
	jobMgr    *jobObjectManager
	processes map[string]ProcessHandle
	closed    atomic.Bool
}

// NewProcessSupervisor creates and initializes a unified process supervisor.
func NewProcessSupervisor() (*ProcessSupervisor, error) {
	jm, err := newJobObjectManager()
	if err != nil {
		return nil, fmt.Errorf("init jobObjectManager: %w", err)
	}

	return &ProcessSupervisor{
		jobMgr:    jm,
		processes: make(map[string]ProcessHandle),
	}, nil
}

// Spawn starts a process according to spec, assigning it to the supervisor's Job Object.
func (s *ProcessSupervisor) Spawn(ctx context.Context, spec ProcessSpec) (ProcessHandle, error) {
	if s.closed.Load() {
		return nil, errors.New("process supervisor is closed")
	}
	if spec.ID == "" {
		return nil, errors.New("spec.ID cannot be empty")
	}

	s.mu.Lock()
	if _, exists := s.processes[spec.ID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("process with ID %q already exists", spec.ID)
	}
	s.mu.Unlock()

	var handle ProcessHandle
	var err error

	if spec.UsePTY {
		handle, err = s.spawnPTY(ctx, spec)
	} else {
		handle, err = s.spawnStd(ctx, spec)
	}

	if err != nil {
		return nil, err
	}

	// Assign to Windows Job Object so child tree is destroyed on exit
	if handle.PID() > 0 && s.jobMgr != nil {
		_ = s.jobMgr.AssignProcess(handle.PID())
	}

	s.mu.Lock()
	s.processes[spec.ID] = handle
	s.mu.Unlock()

	// Auto-remove on exit
	handle.OnExit(func(_ int) {
		s.mu.Lock()
		delete(s.processes, spec.ID)
		s.mu.Unlock()
	})

	return handle, nil
}

// Get returns the process handle by ID if active.
func (s *ProcessSupervisor) Get(id string) (ProcessHandle, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.processes[id]
	return h, ok
}

// Kill terminates a process by ID.
func (s *ProcessSupervisor) Kill(id string) error {
	s.mu.RLock()
	h, ok := s.processes[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("process %q not found", id)
	}
	return h.Kill()
}

// List returns status summaries for all active processes.
func (s *ProcessSupervisor) List() []ProcessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]ProcessInfo, 0, len(s.processes))
	for _, p := range s.processes {
		res = append(res, ProcessInfo{
			ID:       p.ID(),
			Kind:     p.Kind(),
			PID:      p.PID(),
			Running:  p.IsRunning(),
			ExitCode: p.ExitCode(),
		})
	}
	return res
}

// Close kills all active managed processes and closes the Job Object.
func (s *ProcessSupervisor) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.mu.Lock()
	for _, p := range s.processes {
		_ = p.Kill()
	}
	s.processes = make(map[string]ProcessHandle)
	s.mu.Unlock()

	if s.jobMgr != nil {
		return s.jobMgr.Close()
	}
	return nil
}

// --- PTY Process Implementation ---

type ptyProcessHandle struct {
	spec      ProcessSpec
	pty       pty.Pty
	cmd       *pty.Cmd
	running   atomic.Bool
	exitCode  atomic.Int32
	waitDone  chan struct{}
	readDone  chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	history   []byte
	outputCbs []func(data []byte)
	exitCbs   []func(code int)
}

func (h *ptyProcessHandle) closePty() {
	h.closeOnce.Do(func() {
		if h.pty != nil {
			_ = h.pty.Close()
		}
	})
}

func (s *ProcessSupervisor) spawnPTY(ctx context.Context, spec ProcessSpec) (ProcessHandle, error) {
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}

	cols := spec.Cols
	if cols <= 0 {
		cols = 120
	}
	rows := spec.Rows
	if rows <= 0 {
		rows = 30
	}
	_ = p.Resize(cols, rows)

	var cmd *pty.Cmd
	if ctx != nil && ctx.Done() != nil {
		cmd = p.CommandContext(ctx, spec.Command, spec.Args...)
	} else {
		cmd = p.Command(spec.Command, spec.Args...)
	}

	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	if len(spec.Env) > 0 {
		cmd.Env = spec.Env
	}

	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("start pty process: %w", err)
	}

	h := &ptyProcessHandle{
		spec:     spec,
		pty:      p,
		cmd:      cmd,
		waitDone: make(chan struct{}),
		readDone: make(chan struct{}),
	}
	h.running.Store(true)
	h.exitCode.Store(-1)

	// Stream PTY output with 25ms batch buffer (prevents flooding IPC)
	go h.readPtyLoop()
	go h.waitLoop()

	return h, nil
}

func (h *ptyProcessHandle) ID() string         { return h.spec.ID }
func (h *ptyProcessHandle) Kind() ProcessKind  { return h.spec.Kind }
func (h *ptyProcessHandle) PID() int {
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Pid
	}
	return 0
}
func (h *ptyProcessHandle) IsRunning() bool    { return h.running.Load() }
func (h *ptyProcessHandle) ExitCode() int      { return int(h.exitCode.Load()) }

func (h *ptyProcessHandle) Write(data []byte) error {
	if !h.running.Load() || h.pty == nil {
		return errors.New("pty not running")
	}
	_, err := h.pty.Write(data)
	return err
}

func (h *ptyProcessHandle) Resize(cols, rows int) error {
	if h.pty == nil {
		return errors.New("pty is nil")
	}
	return h.pty.Resize(cols, rows)
}

func (h *ptyProcessHandle) Interrupt() error {
	if !h.running.Load() || h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	// Send Ctrl+C
	_, err := h.pty.Write([]byte{0x03})
	return err
}

func (h *ptyProcessHandle) Kill() error {
	h.running.Store(false)
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.closePty()
	return nil
}

func (h *ptyProcessHandle) Wait() error {
	<-h.waitDone
	<-h.readDone
	return nil
}

func (h *ptyProcessHandle) OnOutput(cb func(data []byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outputCbs = append(h.outputCbs, cb)
	if len(h.history) > 0 {
		hist := make([]byte, len(h.history))
		copy(hist, h.history)
		go cb(hist)
	}
}

func (h *ptyProcessHandle) OnExit(cb func(exitCode int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exitCbs = append(h.exitCbs, cb)
}

func (h *ptyProcessHandle) readPtyLoop() {
	buf := make([]byte, 8192)
	var batchMu sync.Mutex
	batch := make([]byte, 0, 16384)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		batchMu.Lock()
		if len(batch) == 0 {
			batchMu.Unlock()
			return
		}
		out := make([]byte, len(batch))
		copy(out, batch)
		batch = batch[:0]
		batchMu.Unlock()

		h.mu.Lock()
		cbs := append([]func(data []byte){}, h.outputCbs...)
		if len(cbs) == 0 {
			if len(h.history) < 64*1024 {
				h.history = append(h.history, out...)
			}
		}
		h.mu.Unlock()

		for _, cb := range cbs {
			cb(out)
		}
	}

	readDone := make(chan struct{})

	go func() {
		defer close(readDone)
		for {
			n, err := h.pty.Read(buf)
			if n > 0 {
				batchMu.Lock()
				batch = append(batch, buf[:n]...)
				shouldFlush := len(batch) >= 16384
				batchMu.Unlock()
				if shouldFlush {
					flush()
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-readDone:
			flush()
			close(h.readDone)
			return
		case <-ticker.C:
			flush()
		}
	}
}

func (h *ptyProcessHandle) waitLoop() {
	defer close(h.waitDone)
	err := h.cmd.Wait()
	h.running.Store(false)

	// Close pty after short delay to allow read loop to flush final output
	time.AfterFunc(80*time.Millisecond, func() {
		h.closePty()
	})

	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	h.exitCode.Store(int32(code))

	h.mu.Lock()
	cbs := append([]func(code int){}, h.exitCbs...)
	h.mu.Unlock()

	for _, cb := range cbs {
		cb(code)
	}
}

// --- Standard (Non-PTY) Process Implementation ---

type stdProcessHandle struct {
	spec      ProcessSpec
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	running   atomic.Bool
	exitCode  atomic.Int32
	waitDone  chan struct{}
	mu        sync.Mutex
	outputCbs []func(data []byte)
	exitCbs   []func(code int)
}

func (s *ProcessSupervisor) spawnStd(ctx context.Context, spec ProcessSpec) (ProcessHandle, error) {
	var cmd *exec.Cmd
	if ctx != nil && ctx.Done() != nil {
		cmd = exec.CommandContext(ctx, spec.Command, spec.Args...)
	} else {
		cmd = exec.Command(spec.Command, spec.Args...)
	}

	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	if len(spec.Env) > 0 {
		cmd.Env = spec.Env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	if spec.Kind != KindTerminal {
		_ = stdin.Close()
	}

	h := &stdProcessHandle{
		spec:     spec,
		cmd:      cmd,
		stdin:    stdin,
		waitDone: make(chan struct{}),
	}
	h.running.Store(true)
	h.exitCode.Store(-1)

	mergedOut := io.MultiReader(stdout, stderr)
	go h.readLoop(mergedOut)
	go h.waitLoop()

	return h, nil
}

func (h *stdProcessHandle) ID() string         { return h.spec.ID }
func (h *stdProcessHandle) Kind() ProcessKind  { return h.spec.Kind }
func (h *stdProcessHandle) PID() int {
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Pid
	}
	return 0
}
func (h *stdProcessHandle) IsRunning() bool    { return h.running.Load() }
func (h *stdProcessHandle) ExitCode() int      { return int(h.exitCode.Load()) }

func (h *stdProcessHandle) Write(data []byte) error {
	if !h.running.Load() || h.stdin == nil {
		return errors.New("process not running")
	}
	_, err := h.stdin.Write(data)
	return err
}

func (h *stdProcessHandle) Resize(cols, rows int) error {
	// Not applicable for non-PTY
	return nil
}

func (h *stdProcessHandle) Interrupt() error {
	if !h.running.Load() || h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	// For standard processes, interrupt can send os.Interrupt
	return h.cmd.Process.Signal(os.Interrupt)
}

func (h *stdProcessHandle) Kill() error {
	h.running.Store(false)
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Kill()
	}
	return nil
}

func (h *stdProcessHandle) Wait() error {
	<-h.waitDone
	return nil
}

func (h *stdProcessHandle) OnOutput(cb func(data []byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outputCbs = append(h.outputCbs, cb)
}

func (h *stdProcessHandle) OnExit(cb func(exitCode int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exitCbs = append(h.exitCbs, cb)
}

func (h *stdProcessHandle) readLoop(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := bytes.Clone(buf[:n])
			h.mu.Lock()
			cbs := append([]func(data []byte){}, h.outputCbs...)
			h.mu.Unlock()
			for _, cb := range cbs {
				cb(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *stdProcessHandle) waitLoop() {
	defer close(h.waitDone)
	err := h.cmd.Wait()
	h.running.Store(false)

	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	h.exitCode.Store(int32(code))

	h.mu.Lock()
	cbs := append([]func(code int){}, h.exitCbs...)
	h.mu.Unlock()

	for _, cb := range cbs {
		cb(code)
	}
}
