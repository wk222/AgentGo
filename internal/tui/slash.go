package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const helpText = `Commands
  /new               start a new conversation
  /sessions          pick a saved conversation (↑/↓, Enter)
  /resume N|ID       switch to a saved conversation
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
