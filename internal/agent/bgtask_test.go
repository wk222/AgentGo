package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
	backgroundtool "github.com/cloudwego/eino/adk/backgroundtask/tool"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackgroundTaskCoordinator_InitAndRun(t *testing.T) {
	tempDir := t.TempDir()
	coord, err := NewBackgroundTaskCoordinator(tempDir, tempDir)
	require.NoError(t, err)
	require.NotNil(t, coord)
	require.NotNil(t, coord.Manager())
	require.NotNil(t, coord.Store())
	require.NotNil(t, coord.Executors())
	require.NotNil(t, coord.Shell())
	require.NotNil(t, coord.ProgressReaders())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Test OSRecoverableShell StartCommand and Updates
	shell := coord.Shell()
	run, err := shell.StartCommand(ctx, &backgroundshell.StartCommandRequest{
		TaskID:  "task_test_1",
		Command: "echo hello-agentgo",
		Attempt: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, run)

	outcome, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, backgroundtask.StatusCompleted, outcome.Status)
	assert.Contains(t, string(outcome.Data), "hello-agentgo")

	// 2. Test RecoverCommand
	recRun, err := shell.RecoverCommand(ctx, &backgroundshell.RecoverCommandRequest{
		TaskID:  "task_test_1",
		Command: "echo hello-agentgo",
		Attempt: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, recRun)
	recOutcome, err := recRun.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, backgroundtask.StatusCompleted, recOutcome.Status)
}

func TestDiskPlantaskBackend(t *testing.T) {
	tempDir := t.TempDir()
	backend := newDiskPlantaskBackend(tempDir)
	ctx := context.Background()

	// 1. Write a task file
	filePath := filepath.Join(tempDir, "1.json")
	err := backend.Write(ctx, &plantask.WriteRequest{
		FilePath: filePath,
		Content:  `{"id":"1","subject":"Build AgentGo","status":"pending"}`,
	})
	require.NoError(t, err)

	// 2. LsInfo
	files, err := backend.LsInfo(ctx, &plantask.LsInfoRequest{Path: tempDir})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, filePath, files[0].Path)

	// 3. Read
	content, err := backend.Read(ctx, &plantask.ReadRequest{FilePath: filePath})
	require.NoError(t, err)
	require.NotNil(t, content)
	assert.Contains(t, content.Content, "Build AgentGo")

	// 4. Delete
	err = backend.Delete(ctx, &plantask.DeleteRequest{FilePath: filePath})
	require.NoError(t, err)

	filesAfter, err := backend.LsInfo(ctx, &plantask.LsInfoRequest{Path: tempDir})
	require.NoError(t, err)
	assert.Empty(t, filesAfter)
}

func TestBuildTypedMiddlewareStack_IncludesBackgroundAndPlanTask(t *testing.T) {
	tempDir := t.TempDir()
	coord, err := NewBackgroundTaskCoordinator(tempDir, tempDir)
	require.NoError(t, err)

	stack, err := BuildTypedMiddlewareStack[*schema.Message](context.Background(), nil, tempDir, tempDir, coord)
	require.NoError(t, err)
	require.NotEmpty(t, stack)
}

func TestBackgroundTaskCoordinator_ProgressReader(t *testing.T) {
	tempDir := t.TempDir()
	coord, err := NewBackgroundTaskCoordinator(tempDir, tempDir)
	require.NoError(t, err)

	readers := coord.ProgressReaders()
	require.NotEmpty(t, readers)
	reader, ok := readers[backgroundtool.RecoverableExecutorKey]
	require.True(t, ok)
	require.NotNil(t, reader)

	// Create a real task in store to test progress reader
	created, err := coord.Store().Create(context.Background(), &backgroundtask.CreateTaskRequest{
		Spec: backgroundtask.Spec{
			ID:          "dummy_task",
			ExecutorKey: backgroundtool.RecoverableExecutorKey,
			Kind:        "background_tool",
		},
		LeaseExpiryPolicy: backgroundtask.LeaseExpiryRetry,
	})
	require.NoError(t, err)

	progress, err := reader.ReadProgress(context.Background(), created)
	require.NoError(t, err)
	assert.Empty(t, progress)
}
