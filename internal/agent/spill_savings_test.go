package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func spillState(content string) *adk.TypedChatModelAgentState[*schema.Message] {
	return &adk.TypedChatModelAgentState[*schema.Message]{Messages: []*schema.Message{
		schema.UserMessage("go"),
		{Role: schema.Tool, Name: "read_workspace_file", ToolCallID: "c1", Content: content},
	}}
}

func spillFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(dir, "spills"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) != "index.json" {
			n++
		}
		return nil
	})
	return n
}

func TestSpillIsCountedInTheRunMeter(t *testing.T) {
	dir := t.TempDir()
	st, err := BuildDefaultSpillStore(dir)
	require.NoError(t, err)
	mw, err := NewSpillMiddleware(st)
	require.NoError(t, err)

	meter := &RunMeter{}
	ctx := WithRunMeter(context.Background(), meter)
	raw := strings.Repeat("a line of grep output that is fairly long\n", 400) // ~17 KB, 400 lines
	state := spillState(raw)
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	require.NoError(t, err)

	u := meter.Snapshot()
	require.Equal(t, 1, u.Spills)
	require.Equal(t, int64(len(raw)), u.SpilledBytes)
	require.Equal(t, int64(len(raw)-len(out.Messages[1].Content)), u.SavedBytes)
	require.Greater(t, u.SavedBytes, int64(len(raw)/2), "a 400-line output must shrink to a small preview")
}

// A big output with few physical lines (a file read comes back as one line of
// JSON) is left alone: previewing it cut the file the model asked for in the
// middle and tripled tool calls in the spill A/B. It also must not be written
// to disk on every model call, which the old code did without ever shortening
// the message.
func TestFewLineOutputIsLeftAloneAndNeverWritten(t *testing.T) {
	dir := t.TempDir()
	st, err := BuildDefaultSpillStore(dir)
	require.NoError(t, err)
	mw, err := NewSpillMiddleware(st)
	require.NoError(t, err)

	meter := &RunMeter{}
	ctx := WithRunMeter(context.Background(), meter)
	raw := `{"rows":[` + strings.Repeat(`{"id":1,"name":"abcdefghij"},`, 1500) + `]}` // ~43 KB, one line
	state := spillState(raw)
	for i := 0; i < 3; i++ { // one call per model iteration; the state persists
		_, _, err = mw.BeforeModelRewriteState(ctx, state, nil)
		require.NoError(t, err)
	}
	require.Equal(t, raw, state.Messages[1].Content)
	require.Equal(t, 0, meter.Snapshot().Spills)
	require.Equal(t, 0, spillFiles(t, dir), "nothing replaced, so nothing may be written")
}

func TestManyLineOutputIsOffloadedOnceAcrossModelCalls(t *testing.T) {
	dir := t.TempDir()
	st, err := BuildDefaultSpillStore(dir)
	require.NoError(t, err)
	mw, err := NewSpillMiddleware(st)
	require.NoError(t, err)

	meter := &RunMeter{}
	ctx := WithRunMeter(context.Background(), meter)
	state := spillState(strings.Repeat("src/some/file.go:123: a matching line of output\n", 300))
	for i := 0; i < 3; i++ {
		_, _, err = mw.BeforeModelRewriteState(ctx, state, nil)
		require.NoError(t, err)
	}
	require.Contains(t, state.Messages[1].Content, "[Offloaded to spill store: spill_")
	require.Equal(t, 1, meter.Snapshot().Spills)
	require.Equal(t, 1, spillFiles(t, dir))
}

func TestOutputThatCannotShrinkIsNotSpilled(t *testing.T) {
	dir := t.TempDir()
	st, err := BuildDefaultSpillStore(dir)
	require.NoError(t, err)
	mw, err := NewSpillMiddleware(st)
	require.NoError(t, err)

	// 52 very short lines: over the line threshold, but the head/tail preview
	// plus its banner is not shorter than the output itself.
	raw := strings.Repeat("x\n", 52)
	meter := &RunMeter{}
	state := spillState(raw)
	_, _, err = mw.BeforeModelRewriteState(WithRunMeter(context.Background(), meter), state, nil)
	require.NoError(t, err)
	require.Equal(t, raw, state.Messages[1].Content, "a preview longer than the output must not replace it")
	require.Equal(t, 0, meter.Snapshot().Spills)
	require.Equal(t, 0, spillFiles(t, dir), "nothing spilled, nothing written")
}
