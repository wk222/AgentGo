package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	cDim    = lipgloss.Color("244")
	cAccent = lipgloss.Color("39")
	cGreen  = lipgloss.Color("42")
	cRed    = lipgloss.Color("203")
	cYellow = lipgloss.Color("214")
	cCyan   = lipgloss.Color("37")

	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sUser    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sTool    = lipgloss.NewStyle().Foreground(cCyan)
	sErr     = lipgloss.NewStyle().Foreground(cRed)
	sWarn    = lipgloss.NewStyle().Foreground(cYellow)
	sOK      = lipgloss.NewStyle().Foreground(cGreen)
	sAdd     = lipgloss.NewStyle().Foreground(cGreen)
	sDel     = lipgloss.NewStyle().Foreground(cRed)
	sHunk    = lipgloss.NewStyle().Foreground(cCyan)
	sStatus  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238"))
	sBadge   = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(cYellow).Bold(true)
	sPanel   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cYellow).Padding(0, 1)
	sPicker  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 1)
	sCurrent = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

// styleDiffLine colours one line of a unified diff or an approval preview.
func styleDiffLine(l string) string {
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		return sDim.Render(l)
	case strings.HasPrefix(l, "@@"):
		return sHunk.Render(l)
	case strings.HasPrefix(l, "+"):
		return sAdd.Render(l)
	case strings.HasPrefix(l, "-"):
		return sDel.Render(l)
	}
	return l
}

// styleDiag colours one diagnostics line by severity.
func styleDiag(l string) string {
	if strings.HasPrefix(strings.ToLower(l), "error") {
		return sErr.Render(l)
	}
	return sWarn.Render(l)
}

// wrap hard-wraps s to width w (display columns).
func wrap(s string, w int) string {
	if w < 1 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}
