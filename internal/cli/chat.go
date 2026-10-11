package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"

	"agentgo/internal/engine"
)

// Chat drives turns against a Backend: it streams a run through the Renderer
// and settles every pause the run hits — approving, denying or answering —
// until the run is finished or a pause cannot be settled here.
type Chat struct {
	B       Backend
	R       *Renderer
	In      *bufio.Reader // where decisions are read from
	Prompt  io.Writer     // where decision prompts are written (stderr)
	Yolo    bool          // approve every tool call without asking
	Ask     bool          // a human can be asked (otherwise pauses are left open)
	Session string

	mu      sync.Mutex
	running bool
}

// Outcome is how a turn ended.
type Outcome struct {
	Error       string
	Interrupted bool // the user cancelled the run
	// Unresolved are pauses that nobody could settle (non-interactive, no yolo).
	Unresolved []Message
}

// OK reports a clean finish.
func (o Outcome) OK() bool { return o.Error == "" && !o.Interrupted && len(o.Unresolved) == 0 }

// EnsureSession creates a conversation on first use.
func (c *Chat) EnsureSession() error {
	if c.Session != "" {
		return nil
	}
	id, err := c.B.NewSession("")
	if err != nil {
		return err
	}
	c.Session = id
	return nil
}

// Cancel stops the run in progress, if any. Safe to call from a signal handler.
func (c *Chat) Cancel() bool {
	c.mu.Lock()
	running, sid := c.running, c.Session
	c.mu.Unlock()
	if running && sid != "" {
		c.B.Cancel(sid)
	}
	return running
}

// Turn sends input and runs to completion, settling pauses on the way.
func (c *Chat) Turn(input string) Outcome {
	if err := c.EnsureSession(); err != nil {
		return Outcome{Error: err.Error()}
	}
	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		c.R.EndTurn()
	}()

	c.R.BeginTurn()
	res := c.B.Run(c.Session, input, c.R)
	if res.Error != "" {
		return Outcome{Error: res.Error}
	}
	if res.Reason == "interrupted" {
		return Outcome{Interrupted: true}
	}
	var pending []Message
	for _, m := range res.Messages {
		msg := Message{Role: m.Role, Type: m.Type, Content: m.Content, ApprovalID: m.ApprovalID,
			ToolName: m.ToolName, Arguments: m.Arguments, Status: m.Status}
		if msg.Pending() {
			pending = append(pending, msg)
		} else if !c.R.Streamed() && msg.Role == "assistant" && (msg.Type == "" || msg.Type == "text") {
			c.R.Message(msg)
		}
	}
	return c.settle(pending)
}

// Resolve decides a pause left behind by an earlier run, then settles whatever
// the resumed run pauses on next.
func (c *Chat) Resolve(p Message, approved bool) Outcome {
	res := c.B.Approve(p.ApprovalID, approved)
	if res.Error != "" {
		return Outcome{Error: res.Error}
	}
	if !approved {
		c.R.Notice("denied " + p.ToolName)
	}
	return c.settleResult(res)
}

func (c *Chat) settleResult(res Result) Outcome {
	var pending []Message
	for _, m := range res.Messages {
		if m.Pending() {
			pending = append(pending, m)
		} else if m.Role == "assistant" {
			c.R.Message(m)
		}
	}
	return c.settle(pending)
}

// settle works through pauses; each decision may produce new ones.
func (c *Chat) settle(pending []Message) Outcome {
	for len(pending) > 0 {
		p := pending[0]
		pending = pending[1:]

		res, handled := c.decide(p)
		if !handled {
			return Outcome{Unresolved: append([]Message{p}, pending...)}
		}
		if res.Error != "" {
			return Outcome{Error: res.Error}
		}
		for _, m := range res.Messages {
			if m.Pending() {
				pending = append(pending, m)
			} else if m.Role == "assistant" {
				c.R.Message(m)
			}
		}
	}
	return Outcome{}
}

// decide settles one pause. handled=false means no one could decide it.
func (c *Chat) decide(p Message) (Result, bool) {
	if p.Type == "question" {
		if !c.Ask {
			return Result{}, false
		}
		answer, ok := c.readAnswer(p.Content)
		if !ok {
			return Result{Error: "no answer given"}, true
		}
		return c.B.Answer(p.ApprovalID, answer), true
	}

	approved := c.Yolo
	if !approved {
		if !c.Ask {
			return Result{}, false
		}
		switch c.askApproval(p) {
		case 'y':
			approved = true
		case 'a':
			approved, c.Yolo = true, true
		}
	} else {
		c.R.Notice("auto-approved " + p.ToolName)
	}
	res := c.B.Approve(p.ApprovalID, approved)
	if !approved && res.Error == "" {
		c.R.Notice("denied " + p.ToolName)
	}
	return res, true
}

// askApproval prompts for a decision: 'y' approve, 'a' approve everything from
// now on, anything else deny. End of input denies.
func (c *Chat) askApproval(p Message) rune {
	c.R.mu.Lock()
	c.R.endLine()
	c.R.mu.Unlock()
	fmt.Fprintf(c.Prompt, "\nAllow %s %s\n", p.ToolName, oneLine(p.Arguments, 300))
	fmt.Fprint(c.Prompt, "[y]es / [n]o / [a]lways (until exit) > ")
	line, err := c.In.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(c.Prompt)
		return 'n'
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return 'y'
	case "a", "always":
		return 'a'
	}
	return 'n'
}

func (c *Chat) readAnswer(question string) (string, bool) {
	c.R.mu.Lock()
	c.R.endLine()
	c.R.mu.Unlock()
	fmt.Fprintf(c.Prompt, "\n? %s\n> ", strings.TrimSpace(question))
	line, err := c.In.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(c.Prompt)
		return "", false
	}
	return strings.TrimSpace(line), true
}

var _ engine.EventSink = (*Renderer)(nil)
