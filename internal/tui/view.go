package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"agentgo/internal/cli"
)

const (
	minWidth, minHeight = 40, 8
	diffMaxLines        = 40
	diagMaxLines        = 8
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var content string
	switch {
	case m.w == 0:
	case m.w < minWidth || m.h < minHeight:
		content = "Window too small"
	default:
		content = lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.statusLine(), m.bottom)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "AgentGo"
	return v
}

// sync fits the widgets to the window and re-renders the timeline when it
// (or the width) changed.
func (m *Model) sync() {
	if m.w == 0 {
		return
	}
	m.in.SetWidth(m.w)
	m.bottom = m.bottomPanel()
	vh := m.h - 1 - lipgloss.Height(m.bottom)
	if vh < 3 {
		vh = 3
	}
	m.vp.SetWidth(m.w)
	m.vp.SetHeight(vh)
	if m.tl.rev != m.renderedRev || m.w != m.renderedW {
		atBottom := m.vp.AtBottom()
		m.vp.SetContent(m.renderEntries(m.w))
		if atBottom {
			m.vp.GotoBottom()
		}
		m.renderedRev, m.renderedW = m.tl.rev, m.w
	}
}

func (m *Model) statusLine() string {
	parts := []string{" AgentGo"}
	if ws := m.workspaceName(); ws != "" {
		parts = append(parts, ws)
	}
	if m.session != "" {
		id := m.session
		if len(id) > 8 {
			id = id[:8]
		}
		parts = append(parts, "session "+id)
	}
	if m.yolo {
		parts = append(parts, "YOLO")
	}
	if m.diagErrs+m.diagWarns > 0 {
		parts = append(parts, fmt.Sprintf("diagnostics %dE %dW", m.diagErrs, m.diagWarns))
	}
	switch m.mode {
	case modeBusy:
		parts = append(parts, spinFrames[m.frame%len(spinFrames)]+" working")
	case modeApprove:
		parts = append(parts, "waiting for approval")
	case modeAsk:
		parts = append(parts, "waiting for your answer")
	}
	return sStatus.Width(m.w).MaxWidth(m.w).Render(strings.Join(parts, "  ·  "))
}

func (m *Model) bottomPanel() string {
	switch {
	case m.help:
		return sPicker.Width(m.w).Render(helpText + "\n\n" + sDim.Render("press any key to close"))
	case m.mode == modeApprove && len(m.queue) > 0:
		return m.approvalPanel(m.queue[0])
	case m.mode == modePick:
		return m.pickPanel()
	}
	hint := sDim.Render(strings.Repeat("─", m.w))
	switch m.mode {
	case modeBusy:
		hint = sDim.Render("Esc cancels the turn · input is kept " + strings.Repeat("─", max(0, m.w-36)))
	case modeAsk:
		if len(m.queue) > 0 {
			hint = sWarn.Render("? " + oneLine(m.queue[0].Content, m.w-4))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, hint, m.in.View())
}

func (m *Model) approvalPanel(p cli.Message) string {
	lines := strings.Split(preview(p.ToolName, p.Arguments), "\n")
	for i, l := range lines {
		lines[i] = styleDiffLine(l)
	}
	title := sWarn.Bold(true).Render("Allow " + p.ToolName + "?")
	footer := sDim.Render("[y] yes   [n] no   [a] always (until exit)")
	if n := len(m.queue) - 1; n > 0 {
		footer += sDim.Render(fmt.Sprintf("   (%d more waiting)", n))
	}
	return sPanel.Width(m.w).Render(title + "\n" + strings.Join(lines, "\n") + "\n" + footer)
}

func (m *Model) pickPanel() string {
	const window = 8
	start := 0
	if m.pick >= window {
		start = m.pick - window + 1
	}
	end := min(len(m.sessions), start+window)
	var b strings.Builder
	b.WriteString(sCurrent.Render("Resume a conversation") + "\n")
	for i := start; i < end; i++ {
		s := m.sessions[i]
		line := fmt.Sprintf("%2d  %s  %d msgs  %s", i+1, s.ID, s.Messages, oneLine(s.Title, 40))
		if i == m.pick {
			b.WriteString(sCurrent.Render("▸ "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString(sDim.Render("↑/↓ choose · Enter resume · Esc close"))
	return sPicker.Width(m.w).Render(b.String())
}

// renderEntries lays the whole timeline out for width w.
func (m *Model) renderEntries(w int) string {
	var parts []string
	for i := range m.tl.entries {
		parts = append(parts, renderEntry(&m.tl.entries[i], w))
	}
	return strings.Join(parts, "\n")
}

func renderEntry(e *entry, w int) string {
	switch e.kind {
	case kUser:
		return indent(sUser.Render("› "), wrap(e.text, w-2), "  ") + "\n"
	case kAssistant:
		if e.closed {
			if e.md == "" || e.mdWidth != w {
				e.md, e.mdWidth = markdown(e.text, w), w
			}
			return e.md + "\n"
		}
		return wrap(e.text, w)
	case kReasoning:
		return indent(sDim.Render("· "), sDim.Render(wrap(e.text, w-2)), "  ")
	case kTool:
		return renderTool(e, w)
	case kDiff:
		return renderDiff(e, w)
	case kNotice:
		return indent(sDim.Render("· "), sDim.Render(wrap(e.text, w-2)), "  ")
	case kErr:
		return indent(sErr.Render("✗ "), sErr.Render(wrap(e.text, w-2)), "  ")
	}
	return e.text
}

func renderTool(e *entry, w int) string {
	head := sTool.Render("▸ "+e.name) + " " + sDim.Render(oneLine(e.text, max(10, w-len(e.name)-6)))
	if !e.done {
		return head
	}
	mark, style := "✓", sOK
	if e.isErr {
		mark, style = "✗", sErr
	}
	summary := oneLine(e.result, max(10, w-6))
	lines, errs, warns, ok := diagnostics(e.result)
	if ok {
		summary = fmt.Sprintf("%d error(s), %d warning(s)", errs, warns)
	}
	out := head + "\n  " + style.Render(mark) + " " + sDim.Render(summary)
	if ok && len(lines) > 0 {
		for i, l := range lines {
			if i == diagMaxLines {
				out += "\n    " + sDim.Render(fmt.Sprintf("… %d more", len(lines)-diagMaxLines))
				break
			}
			out += "\n    " + styleDiag(oneLine(l, max(10, w-6)))
		}
	}
	return out
}

func renderDiff(e *entry, w int) string {
	lines := strings.Split(strings.TrimRight(e.diff, "\n"), "\n")
	var b strings.Builder
	b.WriteString(sTool.Bold(true).Render("± " + e.path))
	for i, l := range lines {
		if i == diffMaxLines {
			b.WriteString("\n" + sDim.Render(fmt.Sprintf("… %d more lines", len(lines)-diffMaxLines)))
			break
		}
		b.WriteString("\n" + styleDiffLine(oneLineKeepSpace(l, w)))
	}
	return b.String()
}

// oneLineKeepSpace truncates a diff line without collapsing its indentation.
func oneLineKeepSpace(s string, w int) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	if rs := []rune(s); len(rs) > w && w > 1 {
		return string(rs[:w-1]) + "…"
	}
	return s
}

// indent puts first before the first line of body and pad before the rest.
func indent(first, body, pad string) string {
	lines := strings.Split(body, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// markdown renders a finished assistant message; any failure falls back to
// the plain wrapped text.
func markdown(s string, w int) string {
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, w-2)))
	if err != nil {
		return wrap(s, w)
	}
	out, err := r.Render(s)
	if err != nil {
		return wrap(s, w)
	}
	return strings.Trim(out, "\n")
}
