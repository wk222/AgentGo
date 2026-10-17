// Package tui is AgentGo's full-screen terminal client: a scrolling timeline,
// an input box, approval and session overlays, built on Bubble Tea. It drives
// the brain through cli.Backend, so it shares the contract (and the tests'
// fakes) with the line-mode CLI; see docs/plan-native-tui.md.
package tui

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"agentgo/internal/cli"
	"agentgo/internal/engine"
)

// Options configure a TUI session.
type Options struct {
	Workspace string
	Session   string // resume this conversation
	Yolo      bool   // approve every tool call without asking
	Thinking  bool   // show the model's reasoning
	Version   string
}

// Historian is implemented by backends that can replay a saved conversation;
// without it /resume starts from an empty timeline.
type Historian interface {
	History(sessionID string) ([]cli.Message, error)
}

type mode int

const (
	modeIdle    mode = iota
	modeBusy         // a run or a decision is in flight
	modeApprove      // waiting for y/n/a on queue[0]
	modeAsk          // waiting for the answer to queue[0]
	modePick         // session picker
)

const tickEvery = 33 * time.Millisecond

type (
	tickMsg    time.Time
	runDoneMsg struct {
		session string
		res     engine.RunResult
	}
	resolvedMsg struct{ res cli.Result }
	sessionsMsg struct {
		list []cli.SessionInfo
		err  error
	}
	historyMsg struct {
		session string
		msgs    []cli.Message
		err     error
	}
)

// Model is the Bubble Tea model.
type Model struct {
	be   cli.Backend
	opts Options

	tl      timeline
	mode    mode
	session string
	queue   []cli.Message // pauses waiting for a decision
	queued  []string      // messages typed while a turn was running
	yolo    bool

	sessions []cli.SessionInfo
	pick     int
	help     bool
	quitHint bool // Ctrl+C pressed once while idle

	diagErrs, diagWarns int // from the latest lsp_diagnostics result

	sink  *sink
	frame int
	vp    viewport.Model
	in    textarea.Model
	w, h  int

	bottom      string // rendered bottom panel (input or overlay), computed by sync
	renderedRev int    // timeline revision currently in the viewport
	renderedW   int
}

// New builds a model over be.
func New(be cli.Backend, opts Options) *Model {
	in := textarea.New()
	st := textarea.DefaultStyles(true)
	st.Cursor.Blink = false // a steady cursor: no idle redraws, no timer commands
	in.SetStyles(st)
	in.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "› "
		}
		return "  "
	})
	in.Placeholder = "Message AgentGo… (Enter sends, Alt+Enter newline, /help)"
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.SetVirtualCursor(true)
	in.SetHeight(3)
	in.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	m := &Model{
		be: be, opts: opts, session: opts.Session, yolo: opts.Yolo,
		sink: &sink{}, in: in, renderedRev: -1,
		vp: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
	}
	m.tl.thinking = opts.Thinking
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.in.Focus()}
	if m.session != "" {
		cmds = append(cmds, m.loadHistory(m.session))
	}
	return tea.Batch(cmds...)
}

// Run starts the full-screen program on the real terminal and blocks until the
// user leaves.
func Run(ctx context.Context, be cli.Backend, opts Options) error {
	_, err := tea.NewProgram(New(be, opts), tea.WithContext(ctx)).Run()
	return err
}

// runHeadless is Run over arbitrary streams, for tests.
func runHeadless(ctx context.Context, m *Model, in io.Reader, out io.Writer) error {
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithWindowSize(100, 30)).Run()
	return err
}

func (m *Model) busy() bool { return m.mode == modeBusy }

func (m *Model) workspaceName() string {
	if m.opts.Workspace == "" {
		return ""
	}
	return filepath.Base(m.opts.Workspace)
}
