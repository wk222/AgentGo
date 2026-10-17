package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitNeedsAnswer = 3   // a tool needed approval (or a question an answer) and nobody could give it
	ExitInterrupted = 130 // Ctrl+C
)

// Version is stamped by the build.
var Version = "dev"

// IO is the process environment, injectable for tests.
type IO struct {
	In         io.Reader
	Out, Err   io.Writer
	InTTY      bool // stdin is an interactive terminal
	Color      bool
	Interrupts <-chan os.Signal // nil = none
	// TUI runs the full-screen client and returns its exit code. When set, an
	// interactive chat on a terminal uses it instead of the line REPL (unless
	// -plain). It is a hook so that this package need not import the TUI.
	TUI func(be Backend, o TUIOptions) int
}

// TUIOptions is what the full-screen client needs from the command line.
type TUIOptions struct {
	Workspace string
	Session   string
	Yolo      bool
	Thinking  bool
}

// StdIO is the real process environment, with Ctrl+C wired up.
func StdIO() IO {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt)
	return IO{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		InTTY: isTerminal(os.Stdin), Color: supportsColor(os.Stderr),
		Interrupts: ch,
	}
}

const usage = `agentgo-cli - AgentGo in the terminal

Usage:
  agentgo-cli [flags]                    interactive chat (full-screen on a terminal)
  agentgo-cli tui [flags]                the full-screen chat explicitly
  agentgo-cli run [flags] "task"         run one task and exit (also: pipe the task on stdin)
  agentgo-cli sessions                   list saved conversations
  agentgo-cli plugins                    list plugins and their state
  agentgo-cli tools                      list registered tools
  agentgo-cli approvals                  list tool calls waiting for a decision
  agentgo-cli approve|deny ID...|all     decide them (an approved run continues)
  agentgo-cli version

Flags (before the task):
  -w, -workspace DIR   directory the agent works in (default: current directory)
  -session ID          continue a saved conversation
  -continue            continue the most recent conversation
  -yolo                approve every tool call without asking
  -json                machine-readable output: one JSON event per line on stdout
  -plain               line-mode chat instead of the full-screen one
  -thinking            show the model's reasoning
  -api responses       use the Responses API (streams reasoning summaries)
  -v                   show the runtime's log lines on stderr (always kept in agentgo.log)

Exit codes: 0 ok, 1 error, 2 usage, 3 approval/answer needed but nobody to ask
(re-run with -yolo or interactively), 130 interrupted.

Model settings come from AgentGo's saved configuration; AZURE_OPENAI_API_KEY and
AZURE_OPENAI_API_ENDPOINT override them for the process.
`

type options struct {
	cfg      Config
	session  string
	cont     bool
	yolo     bool
	json     bool
	thinking bool
	plain    bool
}

func parseFlags(name string, args []string, errW io.Writer) (options, []string, error) {
	var o options
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errW)
	fs.StringVar(&o.cfg.Workspace, "workspace", "", "")
	fs.StringVar(&o.cfg.Workspace, "w", "", "")
	fs.StringVar(&o.session, "session", "", "")
	fs.BoolVar(&o.cont, "continue", false, "")
	fs.BoolVar(&o.yolo, "yolo", false, "")
	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.thinking, "thinking", false, "")
	fs.BoolVar(&o.plain, "plain", false, "")
	fs.StringVar(&o.cfg.API, "api", "", "")
	fs.BoolVar(&o.cfg.Verbose, "v", false, "")
	if err := fs.Parse(args); err != nil {
		return o, nil, err
	}
	if o.session != "" && o.cont {
		return o, nil, fmt.Errorf("-session and -continue are mutually exclusive")
	}
	return o, fs.Args(), nil
}

// Main runs the CLI and returns the process exit code.
func Main(args []string, env IO, open OpenFunc) int {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "-help", "--help":
			fmt.Fprint(env.Out, usage)
			return ExitOK
		case "version", "-version", "--version":
			fmt.Fprintln(env.Out, "agentgo-cli", Version)
			return ExitOK
		}
	}
	cmd := "chat"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "chat", "tui", "run", "sessions", "plugins", "tools", "approvals", "approve", "deny":
			cmd, args = args[0], args[1:]
		default:
			cmd = "run" // agentgo-cli "fix the bug"
		}
	}
	o, rest, err := parseFlags(cmd, args, env.Err)
	if err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(env.Out, usage)
			return ExitOK
		}
		return ExitUsage
	}

	var prompt string
	piped := false
	if cmd == "run" {
		prompt = strings.TrimSpace(strings.Join(rest, " "))
		if !env.InTTY {
			if b, _ := io.ReadAll(env.In); len(strings.TrimSpace(string(b))) > 0 {
				piped = true
				if prompt != "" {
					prompt += "\n\n"
				}
				prompt += strings.TrimSpace(string(b))
			}
		}
		if prompt == "" {
			fmt.Fprintln(env.Err, "run: no task given (pass it as an argument or on stdin)")
			return ExitUsage
		}
	} else if cmd == "approve" || cmd == "deny" {
		if len(rest) == 0 {
			fmt.Fprintf(env.Err, "usage: agentgo-cli %s ID... | all\n", cmd)
			return ExitUsage
		}
	} else if len(rest) > 0 {
		fmt.Fprintf(env.Err, "%s: unexpected argument %q\n", cmd, rest[0])
		return ExitUsage
	}

	// Only commands that work in a directory pin one; the others use whatever
	// workspace AgentGo last had.
	if (cmd == "run" || cmd == "chat" || cmd == "tui") && strings.TrimSpace(o.cfg.Workspace) == "" {
		o.cfg.Workspace, _ = os.Getwd()
	}

	be, err := open(o.cfg)
	if err != nil {
		fmt.Fprintln(env.Err, "agentgo-cli:", err)
		return ExitError
	}
	defer be.Close()

	switch cmd {
	case "sessions":
		return listSessions(be, env)
	case "plugins":
		return listPlugins(be, env)
	case "tools":
		for _, t := range be.Tools() {
			fmt.Fprintln(env.Out, t)
		}
		return ExitOK
	case "approvals":
		return listApprovals(be, env)
	}

	in := bufio.NewReader(env.In)
	chat := &Chat{
		B: be, In: in, Prompt: env.Err, Yolo: o.yolo,
		Ask: env.InTTY && !piped,
		R:   &Renderer{Out: env.Out, Err: env.Err, JSON: o.json, Thinking: o.thinking, Color: env.Color},
	}
	if cmd == "approve" || cmd == "deny" {
		return decide(chat, rest, cmd == "approve", env)
	}
	if err := pickSession(be, chat, o); err != nil {
		fmt.Fprintln(env.Err, "agentgo-cli:", err)
		return ExitError
	}
	if cmd == "run" {
		return runOnce(chat, prompt, env)
	}
	if env.TUI != nil && env.InTTY && !o.plain && !o.json {
		return env.TUI(be, TUIOptions{Workspace: o.cfg.Workspace, Session: chat.Session, Yolo: o.yolo, Thinking: o.thinking})
	}
	if cmd == "tui" {
		fmt.Fprintln(env.Err, "tui: needs an interactive terminal (use `agentgo-cli run` for scripts)")
		return ExitUsage
	}
	return repl(chat, in, env, o)
}

// pickSession applies -session / -continue.
func pickSession(be Backend, c *Chat, o options) error {
	switch {
	case o.session != "":
		c.Session = o.session
	case o.cont:
		list, err := be.Sessions()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("-continue: no saved conversation")
		}
		c.Session = list[0].ID
	}
	return nil
}

// watchInterrupts cancels the current run on Ctrl+C. A second Ctrl+C while the
// run is still winding down (or none was running) ends the process via onExit.
func watchInterrupts(c *Chat, ch <-chan os.Signal, onIdle func()) (stop func()) {
	if ch == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				if c.Cancel() {
					c.R.Notice("cancelling…")
					continue
				}
				onIdle()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func runOnce(c *Chat, prompt string, env IO) int {
	exit := make(chan struct{}, 1)
	stop := watchInterrupts(c, env.Interrupts, func() { exit <- struct{}{} })
	defer stop()

	res := make(chan Outcome, 1)
	go func() { res <- c.Turn(prompt) }()

	var out Outcome
	select {
	case out = <-res:
	case <-exit:
		return ExitInterrupted
	}
	if c.Session != "" && !c.R.JSON {
		fmt.Fprintf(env.Err, "session %s\n", c.Session)
	}
	return exitFor(out, c, env)
}

func exitFor(out Outcome, c *Chat, env IO) int {
	switch {
	case out.Interrupted:
		return ExitInterrupted
	case out.Error != "":
		fmt.Fprintln(env.Err, "error:", out.Error)
		return ExitError
	case len(out.Unresolved) > 0:
		for _, p := range out.Unresolved {
			if p.Type == "question" {
				fmt.Fprintf(env.Err, "the agent asked: %s\n", strings.TrimSpace(p.Content))
				continue
			}
			fmt.Fprintf(env.Err, "approval needed: %s %s\n", p.ToolName, oneLine(p.Arguments, 200))
		}
		fmt.Fprintln(env.Err, "nobody to ask: re-run with -yolo, use the interactive chat, or decide later with `agentgo-cli approve|deny ID` (see `agentgo-cli approvals`)")
		return ExitNeedsAnswer
	}
	return ExitOK
}

func listSessions(be Backend, env IO) int {
	list, err := be.Sessions()
	if err != nil {
		fmt.Fprintln(env.Err, "agentgo-cli:", err)
		return ExitError
	}
	for _, s := range list {
		when := ""
		if s.UpdatedAt > 0 {
			when = time.Unix(s.UpdatedAt, 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(env.Out, "%s\t%s\t%d msgs\t%s\n", s.ID, when, s.Messages, s.Title)
	}
	return ExitOK
}

func listApprovals(be Backend, env IO) int {
	pending := be.Pending()
	for _, p := range pending {
		fmt.Fprintf(env.Out, "%s\t%s\t%s\n", p.ApprovalID, p.ToolName, oneLine(p.Arguments, 120))
	}
	if len(pending) == 0 {
		fmt.Fprintln(env.Err, "nothing is waiting for approval")
	}
	return ExitOK
}

// decide settles pauses left behind by earlier runs ("all" = every one).
func decide(c *Chat, ids []string, approved bool, env IO) int {
	byID := map[string]Message{}
	var all []Message
	for _, p := range c.B.Pending() {
		byID[p.ApprovalID] = p
		all = append(all, p)
	}
	targets := all
	if len(ids) != 1 || ids[0] != "all" {
		targets = nil
		for _, id := range ids {
			p, ok := byID[id]
			if !ok {
				fmt.Fprintf(env.Err, "no pending approval %q (see: agentgo-cli approvals)\n", id)
				return ExitError
			}
			targets = append(targets, p)
		}
	}
	code := ExitOK
	for _, p := range targets {
		out := c.Resolve(p, approved)
		c.R.EndTurn()
		if rc := exitFor(out, c, env); rc != ExitOK {
			code = rc
		}
	}
	return code
}

func listPlugins(be Backend, env IO) int {
	for _, p := range be.Plugins() {
		line := fmt.Sprintf("%-22s %s", p.Name, p.State)
		if p.Error != "" {
			line += "  " + oneLine(p.Error, 100)
		}
		fmt.Fprintln(env.Out, line)
	}
	return ExitOK
}
