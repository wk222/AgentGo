package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHeartbeatManager(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "agentgo_heartbeat_test_"+time.Now().Format("150405"))
	defer os.RemoveAll(tmpDir)
	_ = os.MkdirAll(tmpDir, 0755)

	var mu sync.Mutex
	triggered := 0
	lastGoal := ""

	hm := NewHeartbeatManager(tmpDir, func(goalText string) {
		mu.Lock()
		triggered++
		lastGoal = goalText
		mu.Unlock()
	})
	_ = lastGoal

	// 1. Initial config
	cfg := hm.GetConfig()
	assert.False(t, cfg.Active)
	assert.Equal(t, DefaultHeartbeatInterval, cfg.Interval)

	// 2. Goal read fallback
	assert.Equal(t, EmptyGoalMessage, hm.ReadGoal())

	// 3. Write goal
	assert.NoError(t, hm.WriteGoal("1. 检查测试覆盖率\n2. 整理记忆"))
	assert.Contains(t, hm.ReadGoal(), "检查测试覆盖率")

	// 4. Update config
	assert.NoError(t, hm.UpdateConfig(HeartbeatConfig{Interval: 60, Active: true}))
	assert.True(t, hm.GetConfig().Active)
	assert.Equal(t, 60, hm.GetConfig().Interval)
}
