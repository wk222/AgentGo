// Package embedtui runs the stock Crush TUI against a Crush-protocol server that
// lives in the SAME process, with no socket, TCP port or child process.
//
// The embedder supplies an [http.Handler] that speaks Crush's /v1 protocol (for
// AgentGo: crushproto.Server.Handler()). Requests travel over in-memory pipes
// ([net.Pipe]) between Crush's own HTTP client and that handler, so the TUI,
// `ClientWorkspace` and SSE subscription are exactly the ones used in
// client/server mode.
//
// This package exists because Go forbids importing another module's internal/
// packages; everything the embedder needs is exported here.
package embedtui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"agentgo/third_party/crush/internal/client"
	"agentgo/third_party/crush/internal/config"
	crushlog "agentgo/third_party/crush/internal/log"
	"agentgo/third_party/crush/internal/proto"
	"agentgo/third_party/crush/internal/ui/common"
	"agentgo/third_party/crush/internal/ui/exitbanner"
	ui "agentgo/third_party/crush/internal/ui/model"
	"agentgo/third_party/crush/internal/version"
	"agentgo/third_party/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

// Options configures one embedded TUI session.
type Options struct {
	// Cwd is the project directory (default: the process working directory).
	Cwd string
	// DataDir overrides Crush's data directory ("" = default).
	DataDir string
	Debug   bool
	// YOLO auto-accepts permission prompts.
	YOLO bool
	// SessionID resumes that session; ContinueLast resumes the newest one.
	SessionID    string
	ContinueLast bool
	// Channels are MCP servers to enable as channels.
	Channels []string
	// Version is reported to the server (default: Crush's own version).
	Version string
}

// Run shows the TUI and blocks until the user quits. handler is served over an
// in-memory connection for the lifetime of the call.
func Run(ctx context.Context, handler http.Handler, o Options) error {
	if handler == nil {
		return errors.New("embedtui: nil handler")
	}
	cwd := o.Cwd
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return fmt.Errorf("embedtui: cwd: %w", err)
		}
	}
	if cwd, _ = filepath.Abs(cwd); cwd == "" {
		return errors.New("embedtui: cannot resolve cwd")
	}

	// Nothing may write to stderr while the TUI owns the terminal; the file
	// logger replaces this once the workspace (and so the data dir) is known.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	ln := newMemListener()
	srv := &http.Server{Handler: handler} // no timeouts: SSE streams are long-lived
	served := make(chan struct{})
	go func() { defer close(served); _ = srv.Serve(ln) }()
	defer func() {
		_ = srv.Close()
		_ = ln.Close()
		<-served
	}()

	c := client.NewInProcessClient(cwd, ln.dial)

	ver := o.Version
	if ver == "" {
		ver = version.Version
	}
	pw, err := c.CreateWorkspace(ctx, proto.Workspace{
		Path: cwd, DataDir: o.DataDir, Debug: o.Debug, YOLO: o.YOLO,
		Channels: o.Channels, Version: ver, Env: os.Environ(),
	})
	if err != nil {
		return fmt.Errorf("embedtui: create workspace: %w", err)
	}
	if pw.Config != nil {
		crushlog.Setup(filepath.Join(pw.Config.Options.DataDirectory, "logs", "crush.log"), o.Debug)
	}

	ws := workspace.NewClientWorkspace(c, *pw)
	defer ws.Shutdown()
	if pw.Config.IsConfigured() {
		if err := ws.InitCoderAgent(ctx); err != nil {
			slog.Error("Failed to initialize coder agent", "error", err)
		}
	}

	sessionID := o.SessionID
	com := common.DefaultCommon(ws)
	model := ui.New(com, sessionID, o.ContinueLast)

	var env uv.Environ = os.Environ()
	program := tea.NewProgram(
		model,
		tea.WithEnvironment(env),
		tea.WithContext(ctx),
		tea.WithFilter(ui.NewFilter().Filter),
	)
	go ws.Subscribe(program)

	if _, err := program.Run(); err != nil {
		slog.Error("TUI run error", "error", err)
		return fmt.Errorf("embedtui: TUI crashed: %w", err)
	}

	var banner config.ExitBanner
	if cfg := com.Config(); cfg != nil {
		banner = cfg.Options.TUI.ExitBanner
	}
	tw, _, _ := term.GetSize(os.Stdout.Fd())
	if body := exitbanner.Render(banner, model.CurrentSession(), tw); body != "" {
		fmt.Fprintln(colorprofile.NewWriter(os.Stderr, os.Environ()), body)
	}
	return nil
}

// memListener is a net.Listener whose connections are in-memory pipes.
type memListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newMemListener() *memListener {
	return &memListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

// dial hands the server one end of a fresh pipe and returns the other.
func (l *memListener) dial(ctx context.Context) (net.Conn, error) {
	cc, sc := net.Pipe()
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	select {
	case l.ch <- sc:
		return cc, nil
	case <-ctx.Done():
		_, _ = cc.Close(), sc.Close()
		return nil, ctx.Err()
	case <-l.done:
		_, _ = cc.Close(), sc.Close()
		return nil, net.ErrClosed
	case <-t.C:
		_, _ = cc.Close(), sc.Close()
		return nil, errors.New("embedtui: in-process server not accepting")
	}
}

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "embedtui" }
