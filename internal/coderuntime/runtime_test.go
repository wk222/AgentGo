package coderuntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalProcessRuntime_ExecuteShell(t *testing.T) {
	tempDir := t.TempDir()
	rt, err := NewLocalProcessRuntime(tempDir)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := rt.Execute(ctx, ExecutionRequest{
		Language: LangShell,
		Code:     "echo hello_from_coderuntime",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, strings.TrimSpace(res.Stdout), "hello_from_coderuntime")
}

func TestNewRunScriptTool(t *testing.T) {
	tempDir := t.TempDir()
	rt, err := NewLocalProcessRuntime(tempDir)
	require.NoError(t, err)

	tool, err := NewRunScriptTool(rt)
	require.NoError(t, err)
	require.NotNil(t, tool)
}
