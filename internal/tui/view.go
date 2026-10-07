package tui

import (
	"fmt"
	"strings"
)

// UIState encapsulates the current state displayed on screen.
type UIState struct {
	SessionID    string
	ActiveLane   string
	ModelName    string
	WorkspaceDir string
	TotalTokens  int
	TotalCostUSD float64
	StatusText   string
	IsRunning    bool
	Messages     []UIMessage
	InputPrompt  string
}

// UIMessage represents a formatted message entry in the TUI timeline.
type UIMessage struct {
	Role    string
	Content string
}

// ComposeScreen renders the full UIState into a ScreenBuffer.
func ComposeScreen(state *UIState, width, height int) *ScreenBuffer {
	buf := NewScreenBuffer(width, height)
	if height < 6 || width < 20 {
		buf.SetLine(0, "Terminal window too small")
		return buf
	}

	// 1. Header (Row 0, 1)
	headerTitle := fmt.Sprintf(" %sAgentGo%s | Session: %s | Lane: %s%s%s | Model: %s",
		Bold+FgCyan, Reset, state.SessionID, Bold+FgGreen, state.ActiveLane, Reset, state.ModelName)
	buf.SetLine(0, headerTitle)

	statsLine := fmt.Sprintf(" %sTokens:%s %d | %sCost:%s $%.4f | %sDir:%s %s",
		Dim, Reset, state.TotalTokens, Dim, Reset, state.TotalCostUSD, Dim, Reset, state.WorkspaceDir)
	buf.SetLine(1, statsLine)

	divider := strings.Repeat("─", width)
	buf.SetLine(2, Dim+divider+Reset)

	// 2. Footer / Status (Last 3 rows: height-3, height-2, height-1)
	statusRow := height - 3
	dividerRow := height - 2
	inputRow := height - 1

	statusIndicator := FgGreen + "● Ready" + Reset
	if state.IsRunning {
		statusIndicator = FgYellow + "◐ Thinking / Running..." + Reset
	}
	if state.StatusText != "" {
		statusIndicator += fmt.Sprintf(" (%s)", state.StatusText)
	}
	buf.SetLine(statusRow, " "+statusIndicator)
	buf.SetLine(dividerRow, Dim+divider+Reset)

	promptLine := fmt.Sprintf("%s❯%s %s", Bold+FgCyan, Reset, state.InputPrompt)
	buf.SetLine(inputRow, promptLine)

	// 3. Timeline / Body (Row 3 to statusRow - 1)
	bodyStart := 3
	bodyEnd := statusRow - 1
	bodyHeight := bodyEnd - bodyStart + 1

	if bodyHeight > 0 && len(state.Messages) > 0 {
		var formattedLines []string
		for _, msg := range state.Messages {
			roleTag := FgBlue + "[User]" + Reset
			if msg.Role == "assistant" {
				roleTag = FgGreen + "[Assistant]" + Reset
			} else if msg.Role == "tool" {
				roleTag = FgYellow + "[Tool]" + Reset
			} else if msg.Role == "system" {
				roleTag = FgMagenta + "[System]" + Reset
			}

			lines := strings.Split(msg.Content, "\n")
			if len(lines) > 0 {
				formattedLines = append(formattedLines, fmt.Sprintf(" %s %s", roleTag, lines[0]))
				for _, subLine := range lines[1:] {
					formattedLines = append(formattedLines, "   "+subLine)
				}
			}
			formattedLines = append(formattedLines, "") // blank spacing
		}

		// Scroll to bottom
		startIdx := 0
		if len(formattedLines) > bodyHeight {
			startIdx = len(formattedLines) - bodyHeight
		}

		for r := 0; r < bodyHeight; r++ {
			lineIdx := startIdx + r
			if lineIdx < len(formattedLines) {
				buf.SetLine(bodyStart+r, formattedLines[lineIdx])
			}
		}
	}

	return buf
}
