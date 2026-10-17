package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"agentgo/internal/cli"
	"agentgo/internal/engine"
)

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		m.frame++
		m.drain()
		if m.busy() {
			cmd = tick()
		}
	case runDoneMsg:
		cmd = m.onRunDone(msg)
	case resolvedMsg:
		cmd = m.onResolved(msg.res)
	case sessionsMsg:
		m.onSessions(msg)
	case historyMsg:
		if msg.session == m.session {
			if msg.err != nil {
				m.tl.fail("history: " + msg.err.Error())
			} else {
				m.tl.load(msg.msgs)
			}
		}
	case tea.KeyPressMsg:
		cmd = m.onKey(msg)
	default:
		// Blink and paste messages belong to the input box.
		m.in, cmd = m.in.Update(msg)
	}
	m.sync()
	return m, cmd
}

// drain folds the events the run goroutine produced since the last tick.
func (m *Model) drain() {
	for _, e := range m.sink.drain() {
		m.tl.apply(e)
		if e.Type == engine.EventToolResult {
			out, _ := e.Payload["output"].(string)
			if _, errs, warns, ok := diagnostics(out); ok {
				m.diagErrs, m.diagWarns = errs, warns
			}
		}
	}
}

func (m *Model) onRunDone(msg runDoneMsg) tea.Cmd {
	m.drain()
	if msg.session != "" {
		m.session = msg.session
	}
	res := msg.res
	switch {
	case res.Error != "":
		m.tl.fail(res.Error)
		m.queue, m.queued = nil, nil
	case res.Reason == "interrupted":
		m.tl.notice("cancelled")
		m.queue, m.queued = nil, nil
	default:
		for _, em := range res.Messages {
			cm := cli.Message{Role: em.Role, Type: em.Type, Content: em.Content, ApprovalID: em.ApprovalID,
				ToolName: em.ToolName, Arguments: em.Arguments, Status: em.Status}
			switch {
			case cm.Pending():
				m.queue = append(m.queue, cm)
			case !m.tl.streamed && cm.Role == "assistant" && (cm.Type == "" || cm.Type == "text"):
				m.tl.assistantText(cm.Content)
			}
		}
	}
	m.tl.closeStreams()
	return m.advance()
}

func (m *Model) onResolved(res cli.Result) tea.Cmd {
	m.drain()
	if res.Error != "" {
		m.tl.fail(res.Error)
	}
	for _, cm := range res.Messages {
		switch {
		case cm.Pending():
			m.queue = append(m.queue, cm)
		case !m.tl.streamed && cm.Role == "assistant" && (cm.Type == "" || cm.Type == "text"):
			m.tl.assistantText(cm.Content)
		}
	}
	m.tl.closeStreams()
	return m.advance()
}

// advance moves to the next pause, or back to idle when none is left.
func (m *Model) advance() tea.Cmd {
	if len(m.queue) == 0 {
		m.mode = modeIdle
		if len(m.queued) > 0 { // typed while the turn ran
			next := m.queued[0]
			m.queued = m.queued[1:]
			return m.startRun(next)
		}
		return nil
	}
	p := m.queue[0]
	if p.Type == "question" {
		m.mode = modeAsk
		m.tl.add(entry{kind: kNotice, text: "? " + strings.TrimSpace(p.Content)})
		return nil
	}
	if m.yolo {
		return m.decide(true, "auto-approved")
	}
	m.mode = modeApprove
	return nil
}

// decide settles queue[0] and resumes the run.
func (m *Model) decide(approved bool, note string) tea.Cmd {
	p := m.queue[0]
	m.queue = m.queue[1:]
	if note != "" {
		m.tl.notice(note + " " + p.ToolName)
	}
	m.mode = modeBusy
	m.tl.streamed = false // the continued run streams (or returns) its own text
	be, sk, id := m.be, m.sink, p.ApprovalID
	return tea.Batch(tick(), func() tea.Msg {
		if st, ok := be.(cli.Streamer); ok {
			return resolvedMsg{st.ApproveStream(id, approved, sk)}
		}
		return resolvedMsg{be.Approve(id, approved)}
	})
}

// answer replies to the question at queue[0].
func (m *Model) answer(text string) tea.Cmd {
	p := m.queue[0]
	m.queue = m.queue[1:]
	m.tl.user(text)
	m.mode = modeBusy
	m.tl.streamed = false
	be, sk, id := m.be, m.sink, p.ApprovalID
	return tea.Batch(tick(), func() tea.Msg {
		if st, ok := be.(cli.Streamer); ok {
			return resolvedMsg{st.AnswerStream(id, text, sk)}
		}
		return resolvedMsg{be.Answer(id, text)}
	})
}

func (m *Model) startRun(text string) tea.Cmd {
	m.tl.user(text)
	if m.session == "" {
		// A row insert; done here so Esc can cancel by id from the first moment.
		id, err := m.be.NewSession("")
		if err != nil {
			m.tl.fail("new session: " + err.Error())
			return nil
		}
		m.session = id
	}
	m.tl.streamed = false
	m.mode = modeBusy
	be, sk, sess := m.be, m.sink, m.session
	return tea.Batch(tick(), func() tea.Msg {
		return runDoneMsg{session: sess, res: be.Run(sess, text, sk)}
	})
}

func (m *Model) cancel() {
	if m.session != "" {
		m.be.Cancel(m.session)
	}
	m.tl.notice("cancelling…")
}

func (m *Model) loadHistory(id string) tea.Cmd {
	h, ok := m.be.(Historian)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		msgs, err := h.History(id)
		return historyMsg{session: id, msgs: msgs, err: err}
	}
}

func (m *Model) onSessions(msg sessionsMsg) {
	switch {
	case msg.err != nil:
		m.tl.fail(msg.err.Error())
	case len(msg.list) == 0:
		m.tl.notice("no saved conversations")
	default:
		m.sessions = msg.list
		m.pick = 0
		for i, s := range msg.list {
			if s.ID == m.session {
				m.pick = i
			}
		}
		m.mode = modePick
	}
}

// switchTo makes id the current conversation and replays its history.
func (m *Model) switchTo(id string) tea.Cmd {
	m.session = id
	m.queue = nil
	m.tl.entries = nil
	m.tl.rev++
	m.tl.notice("session " + id)
	m.mode = modeIdle
	return m.loadHistory(id)
}

func (m *Model) onKey(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if s != "ctrl+c" {
		m.quitHint = false
	}
	if m.help { // any key closes the help text
		m.help = false
		return nil
	}
	switch m.mode {
	case modeApprove:
		switch s {
		case "y", "Y", "enter":
			return m.decide(true, "")
		case "a", "A":
			m.yolo = true
			m.tl.notice("yolo on: every tool call is approved from now on")
			return m.decide(true, "")
		case "n", "N", "esc", "ctrl+c":
			return m.decide(false, "denied")
		}
		return nil
	case modePick:
		switch s {
		case "up", "k":
			if m.pick > 0 {
				m.pick--
			}
		case "down", "j":
			if m.pick < len(m.sessions)-1 {
				m.pick++
			}
		case "enter":
			return m.switchTo(m.sessions[m.pick].ID)
		case "esc", "ctrl+c", "q":
			m.mode = modeIdle
		}
		return nil
	}

	switch s {
	case "ctrl+c":
		switch m.mode {
		case modeBusy:
			m.cancel()
		case modeAsk:
			m.queue = nil
			m.mode = modeIdle
			m.tl.notice("question left unanswered")
		default:
			if m.quitHint {
				return tea.Quit
			}
			m.quitHint = true
			m.tl.notice("press Ctrl+C again (or Ctrl+D) to exit")
		}
		return nil
	case "ctrl+d":
		if m.mode == modeIdle && strings.TrimSpace(m.in.Value()) == "" {
			return tea.Quit
		}
	case "esc":
		if m.mode == modeBusy {
			m.cancel()
			return nil
		}
	case "pgup", "pgdown", "ctrl+u", "ctrl+f":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(k)
		return cmd
	case "enter":
		return m.submit()
	}
	var cmd tea.Cmd
	m.in, cmd = m.in.Update(k)
	return cmd
}

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.in.Value())
	if text == "" {
		return nil
	}
	if m.mode == modeBusy {
		if strings.HasPrefix(text, "/") {
			m.tl.notice("commands wait for the turn to end (Esc cancels it)")
			return nil
		}
		m.in.Reset()
		m.queued = append(m.queued, text)
		m.tl.notice("queued, sent when the turn ends: " + oneLine(text, 60))
		return nil
	}
	m.in.Reset()
	if m.mode == modeAsk {
		return m.answer(text)
	}
	if strings.HasPrefix(text, "/") {
		return m.slash(text)
	}
	return m.startRun(text)
}
