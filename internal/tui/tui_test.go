package tui_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agentgo/internal/tui"
)

func TestScreenBuffer_Diff(t *testing.T) {
	b1 := tui.NewScreenBuffer(40, 10)
	b1.SetLine(0, "Line 0")
	b1.SetLine(1, "Line 1")

	b2 := b1.Clone()
	b2.SetLine(1, "Line 1 Changed")
	b2.SetLine(2, "Line 2 Added")

	diffs := tui.ComputeDiff(b1, b2)
	require.Len(t, diffs, 2)
	assert.Equal(t, 1, diffs[0].Row)
	assert.Equal(t, "Line 1 Changed", diffs[0].Content)
	assert.Equal(t, 2, diffs[1].Row)
	assert.Equal(t, "Line 2 Added", diffs[1].Content)
}

func TestDifferentialRenderer_Render(t *testing.T) {
	var out bytes.Buffer
	renderer := tui.NewDifferentialRenderer(&out, 80, 24)

	b1 := tui.NewScreenBuffer(80, 24)
	b1.SetLine(0, "Header")
	b1.SetLine(5, "Body text")

	err := renderer.Render(b1)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Header")
	assert.Contains(t, out.String(), "Body text")

	// Render same buffer -> no output written
	out.Reset()
	err = renderer.Render(b1)
	require.NoError(t, err)
	assert.Empty(t, out.String())

	// Render updated buffer -> only changed lines written
	b2 := b1.Clone()
	b2.SetLine(5, "Body text modified")
	err = renderer.Render(b2)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Body text modified")
	assert.NotContains(t, out.String(), "Header")
}

func TestComposeScreen(t *testing.T) {
	state := &tui.UIState{
		SessionID:    "sess_123",
		ActiveLane:   "main",
		ModelName:    "deepseek-chat",
		WorkspaceDir: "/repo",
		TotalTokens:  1500,
		TotalCostUSD: 0.0025,
		IsRunning:    false,
		Messages: []tui.UIMessage{
			{Role: "user", Content: "Hello AgentGo"},
			{Role: "assistant", Content: "Hello! How can I help you today?"},
		},
		InputPrompt: "Analyze this code",
	}

	buf := tui.ComposeScreen(state, 80, 20)
	require.NotNil(t, buf)
	assert.Equal(t, 20, buf.Height)
	assert.Contains(t, buf.Lines[0], "AgentGo")
	assert.Contains(t, buf.Lines[0], "sess_123")
	assert.Contains(t, buf.Lines[1], "1500")
	assert.Contains(t, buf.Lines[buf.Height-1], "Analyze this code")
}
