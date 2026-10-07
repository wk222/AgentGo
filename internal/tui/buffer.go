package tui

import (
	"strings"
)

// ScreenBuffer represents a 2D text canvas for terminal rendering.
type ScreenBuffer struct {
	Width  int
	Height int
	Lines  []string
}

// NewScreenBuffer creates a buffer of specific dimensions.
func NewScreenBuffer(width, height int) *ScreenBuffer {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = ""
	}
	return &ScreenBuffer{
		Width:  width,
		Height: height,
		Lines:  lines,
	}
}

// SetLine writes a string to a specific row, truncating or padding if needed.
func (b *ScreenBuffer) SetLine(row int, content string) {
	if row < 0 || row >= b.Height {
		return
	}
	b.Lines[row] = content
}

// Clone returns a deep copy of the buffer.
func (b *ScreenBuffer) Clone() *ScreenBuffer {
	newLines := make([]string, len(b.Lines))
	copy(newLines, b.Lines)
	return &ScreenBuffer{
		Width:  b.Width,
		Height: b.Height,
		Lines:  newLines,
	}
}

// Clear blanks all lines in the buffer.
func (b *ScreenBuffer) Clear() {
	for i := range b.Lines {
		b.Lines[i] = ""
	}
}

// Diff computes line-level delta between old buffer and new buffer.
type LineDiff struct {
	Row     int
	Content string
}

// ComputeDiff calculates which lines changed from prev to curr.
func ComputeDiff(prev, curr *ScreenBuffer) []LineDiff {
	if curr == nil {
		return nil
	}
	if prev == nil {
		var diffs []LineDiff
		for row, line := range curr.Lines {
			diffs = append(diffs, LineDiff{Row: row, Content: line})
		}
		return diffs
	}

	var diffs []LineDiff
	maxRows := curr.Height
	if prev.Height > maxRows {
		maxRows = prev.Height
	}

	for row := 0; row < maxRows; row++ {
		prevLine := ""
		if row < len(prev.Lines) {
			prevLine = prev.Lines[row]
		}
		currLine := ""
		if row < len(curr.Lines) {
			currLine = curr.Lines[row]
		}

		if strings.TrimRight(prevLine, " ") != strings.TrimRight(currLine, " ") {
			diffs = append(diffs, LineDiff{Row: row, Content: currLine})
		}
	}

	return diffs
}
