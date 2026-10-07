package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// ANSI escape codes for terminal styling and cursor positioning.
const (
	Reset       = "\033[0m"
	Bold        = "\033[1m"
	Dim         = "\033[2m"
	Italic      = "\033[3m"
	Underline   = "\033[4m"
	FgBlack     = "\033[30m"
	FgRed       = "\033[31m"
	FgGreen     = "\033[32m"
	FgYellow    = "\033[33m"
	FgBlue      = "\033[34m"
	FgMagenta   = "\033[35m"
	FgCyan      = "\033[36m"
	FgWhite     = "\033[37m"
	BgBlack     = "\033[40m"
	BgBlue      = "\033[44m"
	BgDarkGray  = "\033[100m"
	ClearScreen = "\033[2J"
	CursorHome  = "\033[H"
	HideCursor  = "\033[?25l"
	ShowCursor  = "\033[?25h"
)

// MoveTo returns ANSI sequence to position cursor at 1-indexed (row, col).
func MoveTo(row, col int) string {
	return fmt.Sprintf("\033[%d;%dH", row+1, col+1)
}

// ClearLine returns ANSI sequence to clear the entire line.
func ClearLine() string {
	return "\033[2K"
}

// DifferentialRenderer manages differential terminal screen updates without flickering.
type DifferentialRenderer struct {
	mu         sync.Mutex
	writer     io.Writer
	prevBuffer *ScreenBuffer
	width      int
	height     int
}

// NewDifferentialRenderer creates a differential renderer targeting writer.
func NewDifferentialRenderer(w io.Writer, width, height int) *DifferentialRenderer {
	return &DifferentialRenderer{
		writer: w,
		width:  width,
		height: height,
	}
}

// Render calculates the diff between prevBuffer and newBuffer, sending minimal ANSI sequences.
func (r *DifferentialRenderer) Render(nextBuffer *ScreenBuffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if nextBuffer == nil {
		return nil
	}

	diffs := ComputeDiff(r.prevBuffer, nextBuffer)
	if len(diffs) == 0 {
		return nil
	}

	var sb strings.Builder
	for _, diff := range diffs {
		// Move cursor to specific line, clear line, write content
		sb.WriteString(MoveTo(diff.Row, 0))
		sb.WriteString(ClearLine())
		sb.WriteString(diff.Content)
	}

	_, err := io.WriteString(r.writer, sb.String())
	if err != nil {
		return err
	}

	r.prevBuffer = nextBuffer.Clone()
	return nil
}

// ForceRepaint clears the terminal and repaints the full buffer.
func (r *DifferentialRenderer) ForceRepaint(buffer *ScreenBuffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(ClearScreen)
	sb.WriteString(CursorHome)

	for row, line := range buffer.Lines {
		sb.WriteString(MoveTo(row, 0))
		sb.WriteString(ClearLine())
		sb.WriteString(line)
	}

	_, err := io.WriteString(r.writer, sb.String())
	if err != nil {
		return err
	}

	r.prevBuffer = buffer.Clone()
	return nil
}
