package codelsp

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

// notInstalledBackoff is how long a bundled server that is not on PATH is left
// alone before PATH is searched for it again.
const notInstalledBackoff = 30 * time.Second

// supervisor starts language servers on demand (one per server that serves the
// file being touched), hands out their sessions, and stops them.
type supervisor struct {
	root      string
	cat       *catalog
	autoStart bool // whether bundled (not user-configured) servers may start by themselves

	// onChange hears about every session's state and diagnostics changes.
	onChange func(s *session, kind string)

	// lifecycle guards the flags below; startups counts launches in flight so a
	// shutdown can wait for them before it takes its snapshot.
	lifecycle sync.Mutex
	startups  sync.WaitGroup
	stopping  bool
	closed    bool
	runCtx    context.Context
	cancelRun context.CancelFunc
	shutdown  sync.Mutex // serialises stopAll callers

	mu           sync.Mutex
	live         map[string]*session
	missingUntil map[string]time.Time

	now    func() time.Time
	locate func(string) (string, error)
}

type supervisorOptions struct {
	root      string
	overrides Overrides
	noAuto    bool
	onChange  func(s *session, kind string)
}

func newSupervisor(o supervisorOptions) *supervisor {
	runCtx, cancel := context.WithCancel(context.Background())
	return &supervisor{
		root: o.root, cat: buildCatalog(o.overrides), autoStart: !o.noAuto, onChange: o.onChange,
		runCtx: runCtx, cancelRun: cancel,
		live: map[string]*session{}, missingUntil: map[string]time.Time{},
		now: time.Now, locate: exec.LookPath,
	}
}

func (sv *supervisor) tell(s *session, kind string) {
	if sv.onChange != nil {
		sv.onChange(s, kind)
	}
}

// sessions returns the live sessions.
func (sv *supervisor) sessions() []*session {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	out := make([]*session, 0, len(sv.live))
	for _, s := range sv.live {
		out = append(out, s)
	}
	return out
}

func (sv *supervisor) named(name string) (*session, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	s, ok := sv.live[name]
	return s, ok
}

// serving returns a live session that handles path, or nil.
func (sv *supervisor) serving(path string) *session {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for _, s := range sv.sessions() {
		if s.serves(path) {
			return s
		}
	}
	return nil
}

// ensure makes sure every server that can handle path is running. It does
// nothing for a path outside the workspace, for servers already up, or once the
// supervisor is closing.
func (sv *supervisor) ensure(ctx context.Context, path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !within(sv.root, path) {
		return
	}
	sv.lifecycle.Lock()
	if sv.closed || sv.stopping || ctx.Err() != nil {
		sv.lifecycle.Unlock()
		return
	}
	sv.startups.Add(1)
	runCtx := sv.runCtx
	sv.lifecycle.Unlock()
	defer sv.startups.Done()

	var wg sync.WaitGroup
	for name, spec := range sv.cat.specs {
		wg.Go(func() { sv.bringUp(ctx, runCtx, name, spec, path) })
	}
	wg.Wait()
}

func isLive(s ServerState) bool {
	return s == StateReady || s == StateStarting || s == StateDisabled
}

func (sv *supervisor) liveSession(name string) (*session, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	s, ok := sv.live[name]
	return s, ok && isLive(s.current())
}

// drop removes s from the live table if it is still the registered one.
func (sv *supervisor) drop(name string, s *session) {
	sv.mu.Lock()
	if sv.live[name] == s {
		delete(sv.live, name)
	}
	sv.mu.Unlock()
}

// bringUp starts one server for path if it applies and is not running.
func (sv *supervisor) bringUp(ctx, runCtx context.Context, name string, spec ServerSpec, path string) {
	userDefined := sv.cat.user[name]
	if !userDefined && !sv.autoStart {
		return
	}
	if s, ok := sv.liveSession(name); ok {
		// Someone else is launching it: wait, so the caller does not query a
		// server that is still initialising.
		s.awaitSettled(ctx)
		sv.tell(s, "state_changed")
		return
	}
	switch {
	case userDefined && !spec.serves(path, sv.root):
		return
	case !userDefined && !sv.worthStarting(name, spec, path):
		return
	}

	s, err := newSession(name, spec, sv.root, sv.tell)
	if err != nil {
		slog.Error("lsp: cannot create session", "server", name, "error", err)
		return
	}
	if !sv.publish(ctx, runCtx, name, s) {
		return
	}
	defer func() {
		s.markSettled()
		sv.tell(s, "state_changed")
	}()

	// The handshake runs on the supervisor's context, not the caller's: a tool
	// call can end long before the server finishes starting, and the server must
	// outlive it.
	initCtx, cancel := context.WithTimeout(runCtx, time.Duration(cmp.Or(spec.TimeoutSec, 30))*time.Second)
	defer cancel()
	if err := s.handshake(initCtx); err != nil {
		slog.Error("lsp: server failed to initialise", "server", name, "error", err)
		sv.abandon(name, s)
		return
	}
	if err := s.awaitRunning(initCtx); err != nil {
		slog.Warn("lsp: server did not become ready", "server", name, "error", err)
		sv.abandon(name, s)
	}
}

// publish registers s as the live session for name. It reports false (and
// kills s) when the supervisor is shutting down or another launch won the race.
func (sv *supervisor) publish(ctx, runCtx context.Context, name string, s *session) bool {
	sv.mu.Lock()
	if runCtx.Err() != nil {
		sv.mu.Unlock()
		s.kill()
		return false
	}
	if other, ok := sv.live[name]; ok && isLive(other.current()) {
		sv.mu.Unlock()
		s.kill()
		other.awaitSettled(ctx)
		sv.tell(other, "state_changed")
		return false
	}
	s.setState(StateStarting)
	sv.live[name] = s
	sv.mu.Unlock()
	return true
}

// abandon kills a session that failed to start and forgets it.
func (sv *supervisor) abandon(name string, s *session) {
	s.kill()
	s.setState(StateError)
	sv.drop(name, s)
}

// worthStarting applies the extra checks a bundled server must pass before it is
// launched unprompted: not a generic launcher, applicable here, and installed.
func (sv *supervisor) worthStarting(name string, spec ServerSpec, path string) bool {
	if launcherCommands[spec.Command] || !spec.serves(path, sv.root) {
		return false
	}
	sv.mu.Lock()
	until, missing := sv.missingUntil[name]
	sv.mu.Unlock()
	if missing && sv.now().Before(until) {
		return false
	}
	if _, err := sv.locate(spec.Command); err != nil {
		slog.Debug("lsp: server not installed", "server", name, "command", spec.Command)
		sv.mu.Lock()
		sv.missingUntil[name] = sv.now().Add(notInstalledBackoff)
		sv.mu.Unlock()
		return false
	}
	sv.mu.Lock()
	delete(sv.missingUntil, name)
	sv.mu.Unlock()
	return true
}

// close permanently stops new launches and stops every server.
func (sv *supervisor) close(ctx context.Context) {
	sv.lifecycle.Lock()
	sv.closed = true
	sv.lifecycle.Unlock()
	sv.stopAll(ctx)
}

// stopAll stops every server and waits for launches in flight. The supervisor
// can start servers again afterwards.
func (sv *supervisor) stopAll(ctx context.Context) {
	sv.shutdown.Lock()
	defer sv.shutdown.Unlock()

	sv.lifecycle.Lock()
	sv.stopping = true
	sv.cancelRun()
	sv.lifecycle.Unlock()

	// A launch owns its process before it publishes it, so wait for launches to
	// finish before snapshotting; none can appear after the snapshot.
	sv.startups.Wait()
	defer func() {
		sv.lifecycle.Lock()
		sv.runCtx, sv.cancelRun = context.WithCancel(context.Background())
		sv.stopping = false
		sv.lifecycle.Unlock()
	}()

	sv.mu.Lock()
	doomed := sv.live
	sv.live = map[string]*session{}
	sv.mu.Unlock()

	var wg sync.WaitGroup
	for name, s := range doomed {
		wg.Go(func() {
			defer sv.tell(s, "state_changed")
			if err := s.shutdown(ctx); err != nil && !benignStopError(err) {
				slog.Warn("lsp: error while stopping", "server", name, "error", err)
			}
			s.cancel()
			s.setState(StateStopped)
		})
	}
	wg.Wait()
}

// benignStopError reports errors that merely mean "the server is already gone".
func benignStopError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
		errors.Is(err, jsonrpc2.ErrClosed) || err.Error() == "signal: killed"
}
