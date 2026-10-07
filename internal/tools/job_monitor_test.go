package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestJobMonitorTool(t *testing.T) {
	r := NewRegistry()
	assert.NoError(t, RegisterJobMonitorTool(r, t.TempDir()))

	var mu sync.Mutex
	wokenUp := false
	var wokenJob *MonitoredJob

	SetJobWakeupHandler(func(j *MonitoredJob) {
		mu.Lock()
		wokenUp = true
		wokenJob = j
		mu.Unlock()
	})

	// 1. Start a quick simulated experiment command
	startJSON := `{
		"action": "start",
		"name": "Simulated_Eval",
		"command": "Write-Output 'Epoch 1/1: Loss=0.042 Accuracy=98.5% -> Training Completed'",
		"summary_prompt": "分析最终验证集准确率并给出结论"
	}`
	res, err := r.InvokeJSON(context.Background(), "job_monitor", startJSON)
	assert.NoError(t, err)
	assert.Contains(t, res, "已在后台静默挂载")

	// Wait for process execution and callback
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return wokenUp && wokenJob != nil && wokenJob.Status == "completed"
	}, 5*time.Second, 100*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "completed", wokenJob.Status)
	assert.Contains(t, wokenJob.LastOutputTail, "Loss=0.042")
	assert.Equal(t, 0, wokenJob.ExitCode)
	mu.Unlock()

	// 2. Query status
	statusJSON := `{"action":"status", "job_id":"` + wokenJob.ID + `"}`
	resStatus, err := r.InvokeJSON(context.Background(), "job_monitor", statusJSON)
	assert.NoError(t, err)
	assert.Contains(t, resStatus, "completed")

	// 3. List
	resList, err := r.InvokeJSON(context.Background(), "job_monitor", `{"action":"list"}`)
	assert.NoError(t, err)
	assert.Contains(t, resList, "Simulated_Eval")
}
