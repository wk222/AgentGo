package ideruntime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// TerminalInfo represents metadata for an active terminal session.
type TerminalInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	PID       int       `json:"pid"`
	Shell     string    `json:"shell"`
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
}

// TerminalService manages multiple interactive terminal instances.
type TerminalService struct {
	mu         sync.RWMutex
	supervisor *ProcessSupervisor
	terminals  map[string]*terminalSession
	dataCbs    []func(termID string, data string)
	exitCbs    []func(termID string, exitCode int)
}

type terminalSession struct {
	info   TerminalInfo
	handle ProcessHandle
	bufMu  sync.Mutex
	buf    []byte
}

// NewTerminalService creates an interactive terminal manager using the supervisor.
func NewTerminalService(supervisor *ProcessSupervisor) *TerminalService {
	return &TerminalService{
		supervisor: supervisor,
		terminals:  make(map[string]*terminalSession),
	}
}

// DefaultShell discovers the best available interactive shell for the OS.
func DefaultShell() string {
	if runtime.GOOS == "windows" {
		// Preference: PowerShell Core (pwsh) -> Windows PowerShell -> cmd.exe
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			return p
		}
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			return p
		}
		if p, err := exec.LookPath("cmd.exe"); err == nil {
			return p
		}
		return "cmd.exe"
	}

	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "/bin/sh"
}

// CreateTerminal spawns a new pseudo-terminal process with the given dimensions.
func (s *TerminalService) CreateTerminal(id, shell, cwd string, cols, rows int) (TerminalInfo, error) {
	if s.supervisor == nil {
		return TerminalInfo{}, errors.New("process supervisor is nil")
	}

	if id == "" {
		id = fmt.Sprintf("term-%d", time.Now().UnixNano())
	}
	if shell == "" {
		shell = DefaultShell()
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}

	spec := ProcessSpec{
		ID:      id,
		Kind:    KindTerminal,
		Command: shell,
		Cwd:     cwd,
		UsePTY:  true,
		Cols:    cols,
		Rows:    rows,
	}

	handle, err := s.supervisor.Spawn(nil, spec)
	if err != nil {
		return TerminalInfo{}, fmt.Errorf("spawn terminal: %w", err)
	}

	info := TerminalInfo{
		ID:        id,
		Title:     shell,
		PID:       handle.PID(),
		Shell:     shell,
		Cwd:       cwd,
		CreatedAt: time.Now(),
	}

	sess := &terminalSession{
		info:   info,
		handle: handle,
	}

	s.mu.Lock()
	s.terminals[id] = sess
	s.mu.Unlock()

	// Forward buffered PTY output to subscribers
	handle.OnOutput(func(data []byte) {
		sess.bufMu.Lock()
		if len(sess.buf) < 128*1024 {
			sess.buf = append(sess.buf, data...)
		}
		sess.bufMu.Unlock()
		s.emitData(id, string(data))
	})

	handle.OnExit(func(exitCode int) {
		s.mu.Lock()
		delete(s.terminals, id)
		s.mu.Unlock()
		s.emitExit(id, exitCode)
	})

	return info, nil
}

// WriteTerminal sends keyboard input or control characters to the PTY.
func (s *TerminalService) WriteTerminal(id, data string) error {
	s.mu.RLock()
	sess, ok := s.terminals[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("terminal %q not found", id)
	}
	return sess.handle.Write([]byte(data))
}

// PollTerminal drains and returns any pending buffered output for the terminal.
func (s *TerminalService) PollTerminal(id string) string {
	s.mu.RLock()
	sess, ok := s.terminals[id]
	s.mu.RUnlock()
	if !ok {
		return ""
	}
	sess.bufMu.Lock()
	defer sess.bufMu.Unlock()
	if len(sess.buf) == 0 {
		return ""
	}
	out := string(sess.buf)
	sess.buf = sess.buf[:0]
	return out
}

// ResizeTerminal informs the OS PTY of window geometry changes.
func (s *TerminalService) ResizeTerminal(id string, cols, rows int) error {
	s.mu.RLock()
	sess, ok := s.terminals[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("terminal %q not found", id)
	}
	return sess.handle.Resize(cols, rows)
}

// CloseTerminal terminates the specified terminal process.
func (s *TerminalService) CloseTerminal(id string) error {
	s.mu.RLock()
	sess, ok := s.terminals[id]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	return sess.handle.Kill()
}

// ListTerminals returns a snapshot of active terminals.
func (s *TerminalService) ListTerminals() []TerminalInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]TerminalInfo, 0, len(s.terminals))
	for _, sess := range s.terminals {
		res = append(res, sess.info)
	}
	return res
}

// OnTerminalData registers a listener for incoming terminal stream data.
func (s *TerminalService) OnTerminalData(cb func(termID string, data string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataCbs = append(s.dataCbs, cb)
}

// OnTerminalExit registers a listener for terminal exit notifications.
func (s *TerminalService) OnTerminalExit(cb func(termID string, exitCode int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exitCbs = append(s.exitCbs, cb)
}

func (s *TerminalService) emitData(termID string, data string) {
	s.mu.RLock()
	cbs := append([]func(termID string, data string){}, s.dataCbs...)
	s.mu.RUnlock()

	for _, cb := range cbs {
		cb(termID, data)
	}
}

func (s *TerminalService) emitExit(termID string, exitCode int) {
	s.mu.RLock()
	cbs := append([]func(termID string, exitCode int){}, s.exitCbs...)
	s.mu.RUnlock()

	for _, cb := range cbs {
		cb(termID, exitCode)
	}
}
