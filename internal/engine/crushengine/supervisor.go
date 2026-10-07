package crushengine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Supervisor owns a `crush server` child process bound to a loopback TCP port.
// It is started lazily by Ensure and transparently restarted if it died
// (crash, or Crush's own idle shutdown when its last workspace was removed).
type Supervisor struct {
	Exe          string        // path to crush executable (required)
	Env          []string      // extra KEY=VALUE entries appended to os.Environ()
	DataDir      string        // optional --data-dir
	LogPath      string        // server stdout/stderr log (default: temp dir)
	StartTimeout time.Duration // default 30s

	mu   sync.Mutex
	cmd  *exec.Cmd
	addr string
	done chan struct{} // closed when the current process has exited
	gen  int           // increments on every (re)start
	log  *os.File
}

func freeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func (s *Supervisor) alive() bool {
	if s.done == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// Alive reports whether the server process is currently running.
func (s *Supervisor) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive()
}

// Ensure starts the server if needed and returns its address and generation.
// A changed generation means previously created workspaces/sessions are gone.
func (s *Supervisor) Ensure(ctx context.Context) (addr string, gen int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alive() {
		return s.addr, s.gen, nil
	}
	if err := s.startLocked(ctx); err != nil {
		return "", 0, err
	}
	return s.addr, s.gen, nil
}

func (s *Supervisor) startLocked(ctx context.Context) error {
	if s.Exe == "" {
		return errors.New("crushengine: Exe is empty")
	}
	if _, err := os.Stat(s.Exe); err != nil {
		return fmt.Errorf("crushengine: crush executable not found: %w", err)
	}
	addr, err := freeLoopbackAddr()
	if err != nil {
		return err
	}
	args := []string{"server", "-H", "tcp://" + addr}
	if s.DataDir != "" {
		args = append(args, "--data-dir", s.DataDir)
	}
	cmd := exec.Command(s.Exe, args...)
	cmd.Env = append(os.Environ(), s.Env...)
	hideWindow(cmd)

	if s.log != nil {
		_ = s.log.Close()
		s.log = nil
	}
	logPath := s.LogPath
	if logPath == "" {
		logPath = filepath.Join(os.TempDir(), "agentgo-crush-server.log")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		s.log = f
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("crushengine: start crush server: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	s.cmd, s.addr, s.done = cmd, addr, done
	s.gen++

	timeout := s.StartTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cl := NewClient(addr, "supervisor-probe")
	for {
		if err := cl.Health(hctx); err == nil {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("crushengine: crush server exited during startup (see %s)", logPath)
		case <-hctx.Done():
			s.stopLocked()
			return fmt.Errorf("crushengine: crush server not healthy within %s (see %s)", timeout, logPath)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Supervisor) stopLocked() {
	if s.cmd != nil && s.cmd.Process != nil && s.alive() {
		killTree(s.cmd)
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
	}
}

// Stop terminates the server (and its children). Safe to call repeatedly.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	if s.log != nil {
		_ = s.log.Close()
		s.log = nil
	}
}
