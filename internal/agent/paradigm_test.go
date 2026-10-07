package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParadigmEngine(t *testing.T) {
	tmpDir := t.TempDir()
	rulesFile := filepath.Join(tmpDir, "RULES.md")
	_ = os.WriteFile(rulesFile, []byte("Strict verification first."), 0644)

	pe := NewParadigmEngine("", map[string]string{
		"@RULES": rulesFile,
	})

	// 1. Add short-term memo
	pe.AddMemo("用户刚才请求升级Eino并移植purrcat功能")

	// 2. OnBuildSystemPrompt
	prompts := pe.OnBuildSystemPrompt()
	assert.NotEmpty(t, prompts)
	foundRules := false
	foundMemo := false
	for _, p := range prompts {
		if assert.ObjectsAreEqual(true, len(p) > 0) {
			if contains(p, "Strict verification first") {
				foundRules = true
			}
			if contains(p, "用户刚才请求升级Eino") {
				foundMemo = true
			}
		}
	}
	assert.True(t, foundRules)
	assert.True(t, foundMemo)

	// 3. OnLoopStart
	startPrompts := pe.OnLoopStart()
	assert.NotEmpty(t, startPrompts)
	assert.Contains(t, startPrompts[0], "先规划TODO")

	// 4. OnLoopEpoch: delay=5, interval=10
	epoch1 := pe.OnLoopEpoch(1)
	assert.Empty(t, epoch1)

	epoch5 := pe.OnLoopEpoch(5)
	assert.NotEmpty(t, epoch5)
	assert.Contains(t, epoch5[0], "Search 工具或记忆系统")

	epoch10 := pe.OnLoopEpoch(10)
	assert.NotEmpty(t, epoch10)
	assert.Contains(t, epoch10[0], "防止执行发散跑偏")

	// 5. OnLoopEnd: expects 'remember' tool check
	passed, hint := pe.OnLoopEnd([]string{"list_workspace", "read_file"})
	assert.False(t, passed)
	assert.Contains(t, hint, "remember 工具")

	passed2, _ := pe.OnLoopEnd([]string{"remember", "read_file"})
	assert.True(t, passed2)

	// 6. OnToolCalling: vision_advisor check
	tip := pe.OnToolCalling("vision_advisor")
	assert.Contains(t, tip, "视觉顾问")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
