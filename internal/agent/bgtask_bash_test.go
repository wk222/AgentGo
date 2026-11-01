package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	"github.com/stretchr/testify/require"
)

// Three independent commands started in one go must overlap, return their IDs
// without waiting, and each end up completed with its own result.
func TestStartBashRunsCommandsConcurrently(t *testing.T) {
	dir := t.TempDir()
	coord, err := NewBackgroundTaskCoordinator(dir, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = coord.Close() })

	const each = 400 * time.Millisecond
	var running, peak int32
	work := func(name string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			n := atomic.AddInt32(&running, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(each)
			atomic.AddInt32(&running, -1)
			return "done:" + name, nil
		}
	}

	ctx := context.Background()
	start := time.Now()
	ids := map[string]string{}
	for _, name := range []string{"A", "B", "C"} {
		id, err := coord.StartBash(ctx, "cmd "+name, work(name))
		require.NoError(t, err)
		require.NotEmpty(t, id)
		ids[name] = id
	}
	require.Less(t, time.Since(start), each, "StartBash must return before the command finishes")

	for name, id := range ids {
		var task *backgroundtask.Task
		require.Eventually(t, func() bool {
			task, err = coord.Manager().Get(ctx, id)
			return err == nil && task != nil && task.Status == backgroundtask.StatusCompleted
		}, 5*time.Second, 20*time.Millisecond, "task %s did not complete", name)
		require.Contains(t, string(task.ResultData), "done:"+name)
	}
	require.Less(t, time.Since(start), 3*each, "three %v commands took as long as running them one after another", each)
	require.GreaterOrEqual(t, atomic.LoadInt32(&peak), int32(2), "commands never overlapped")
}

func TestStartBashWithoutCoordinatorFails(t *testing.T) {
	var c *BackgroundTaskCoordinator
	_, err := c.StartBash(context.Background(), "echo x", func(context.Context) (string, error) { return "", nil })
	require.Error(t, err)
}

// A command that ends reports itself with the session that started it and its
// real task ID, so the wake-up can name it and skip it if the agent read it.
func TestStartBashReportsItsEndWithSessionAndID(t *testing.T) {
	dir := t.TempDir()
	coord, err := NewBackgroundTaskCoordinator(dir, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = coord.Close() })

	got := make(chan BackgroundDone, 1)
	coord.SetDoneHandler(func(d BackgroundDone) { got <- d })

	ctx := WithSessionID(context.Background(), "sess-42")
	id, err := coord.StartBash(ctx, "echo hi", func(context.Context) (string, error) {
		return `{"stdout":"hi","stderr":"","exit_code":0}`, nil
	})
	require.NoError(t, err)

	select {
	case d := <-got:
		require.Equal(t, "sess-42", d.SessionID)
		require.Equal(t, id, d.ID)
		require.Equal(t, "completed", d.Status)
		require.Contains(t, d.Output, "hi")
	case <-time.After(3 * time.Second):
		t.Fatal("no completion report")
	}
}
