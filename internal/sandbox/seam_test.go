package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalExecutionSeam_ExecuteStreaming(t *testing.T) {
	tempDir := t.TempDir()
	seam := NewLocalExecutionSeam()
	assert.Equal(t, ModeLocal, seam.Mode())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var chunks []string
	res, err := seam.ExecuteStreaming(ctx, CommandRequest{
		Command:       "echo seam_stream_test",
		WorkspaceRoot: tempDir,
	}, func(kind string, chunk []byte) {
		chunks = append(chunks, string(chunk))
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, strings.TrimSpace(res.Stdout), "seam_stream_test")
	assert.NotEmpty(t, chunks)
}

func TestLocalExecutionSeam_ValidatePath(t *testing.T) {
	seam := NewLocalExecutionSeam()
	root := "/workspace/myproject"

	// Valid subpath
	err := seam.ValidatePath(root, "/workspace/myproject/src/main.go", true)
	assert.NoError(t, err)

	// Valid relative path
	err = seam.ValidatePath(root, "src/main.go", true)
	assert.NoError(t, err)

	// Invalid escape
	err = seam.ValidatePath(root, "/etc/passwd", true)
	assert.ErrorIs(t, err, ErrPathEscapesWorkspace)
}
