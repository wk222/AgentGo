package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"agentgo/internal/cli"
)

const helpText = `Commands
  /new               start a new conversation
  /sessions          pick a saved conversation (↑/↓, Enter)
  /resume N|ID       switch to a saved conversation
  /fork              copy this conversation into a new one and continue there
  /review [focus]    review the uncommitted changes (what to look at is optional)
  /undo [all|call]   undo the files the agent changed in this conversation (latest step by default)
  /yolo [on|off]     approve every tool call without asking
  /thinking [on|off] show the model's reasoning
  /plugins           list plugins
  /tools             list registered tools
  /help              this text
  /exit              leave (Ctrl+D also works)

Keys
  Enter send · Alt+Enter newline · PgUp/PgDn scroll
  Esc or Ctrl+C cancel the running turn · Ctrl+C twice exit
  approval: y yes · n no · a always (until exit)`

// slash runs one "/" command.
func (m *Model) slash(line string) tea.Cmd {
	f := strings.Fields(line)
	cmd, arg := f[0], strings.Join(f[1:], " ")
	switch cmd {
	case "/exit", "/quit", "/q":
		return tea.Quit
	case "/help", "/?":
		m.help = true
	case "/new":
		m.session = ""
		m.queue = nil
		m.tl.entries = nil
		m.tl.rev++
		m.tl.notice("new conversation (created on your first message)")
	case "/sessions":
		be := m.be
		return func() tea.Msg {
			list, err := be.Sessions()
			return sessionsMsg{list: list, err: err}
		}
	case "/resume":
		id := arg
		if n, err := strconv.Atoi(arg); err == nil {
			if n < 1 || n > len(m.sessions) {
				m.tl.notice("no such entry; run /sessions first")
				return nil
			}
			id = m.sessions[n-1].ID
		}
		if id == "" {
			m.tl.notice("usage: /resume N|ID")
			return nil
		}
		return m.switchTo(id)
	case "/fork":
		f, ok := m.be.(cli.Forker)
		switch {
		case !ok:
			m.tl.notice("this backend cannot fork a conversation")
		case m.session == "":
			m.tl.notice("nothing to fork yet: send a message first")
		default:
			from := m.session
			id, err := f.Fork(from)
			if err != nil {
				m.tl.fail("fork: " + err.Error())
				return nil
			}
			cmd := m.switchTo(id)
			note := "forked from " + from + " (the original is unchanged)"
			if cmd == nil { // no history to wait for
				m.tl.notice(note)
			} else {
				m.afterLoad = note
			}
			return cmd
		}
	case "/review", "/undo":
		// Not handled here: the backend turns "/review ..." and "/undo ..." into
		// instructions (core.ExpandCommand), so the line is sent as a message.
		return m.startRun(line)
	case "/yolo":
		m.yolo = toggle(arg, m.yolo)
		m.tl.notice("yolo " + onOff(m.yolo))
	case "/thinking":
		m.tl.thinking = toggle(arg, m.tl.thinking)
		m.tl.notice("thinking " + onOff(m.tl.thinking))
	case "/plugins":
		var b strings.Builder
		for _, p := range m.be.Plugins() {
			b.WriteString(p.Name + "  " + p.State)
			if p.Error != "" {
				b.WriteString("  " + oneLine(p.Error, 80))
			}
			b.WriteString("\n")
		}
		m.tl.notice(strings.TrimRight(b.String(), "\n"))
	case "/tools":
		m.tl.notice(strings.Join(m.be.Tools(), "\n"))
	default:
		m.tl.notice("unknown command " + cmd + " (try /help)")
	}
	return nil
}

func toggle(arg string, cur bool) bool {
	switch strings.ToLower(arg) {
	case "on", "1", "true", "yes":
		return true
	case "off", "0", "false", "no":
		return false
	}
	return !cur
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
