package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live check of the whole wake-up path with the user's saved model: the agent
// starts a command in the background, ends its turn, and the finished command
// starts a new turn in the same session by itself.
//
//	$env:AGENTGO_LIVE_WAKE = "1"; go test ./internal/bridge -run TestLiveBackgroundWake -v -timeout 10m
//
// Only the saved LLM settings are copied; everything else lives in a temp dir.
func TestLiveBackgroundWake(t *testing.T) {
	if os.Getenv("AGENTGO_LIVE_WAKE") != "1" {
		t.Skip("set AGENTGO_LIVE_WAKE=1 to run against the saved model")
	}
	saved, err := os.ReadFile(filepath.Join(DefaultDataDir(), "config.json"))
	if err != nil {
		t.Skip("no saved config.json:", err)
	}
	data, ws := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "config.json"), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	rt, err := newRuntimeAt(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if _, err := rt.SetWorkspaceRoot(ws); err != nil {
		t.Fatal(err)
	}
	cfg := rt.LLMConfig()
	if cfg.APIKey == "" {
		t.Skip("saved config has no api key")
	}
	t.Logf("model=%s", cfg.Model)

	ctx := context.Background()
	s, err := rt.Sessions().Create(ctx, "wake live")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "请用 execute_bash 在后台运行这条命令（必须设置 run_in_background=true）：" +
		"pwsh -NoProfile -Command \"Start-Sleep -Seconds 10; Set-Content -Path done.txt -Value finished-ok\"。" +
		"启动之后不要等待它、不要调用 task_output，立刻结束本轮，只回复“已在后台启动”。"
	start := time.Now()
	res, err := rt.AgentRunner().GenerateStream(ctx, rt.LLMConfig().agentSettings(), s.ID, prompt, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn 1 ended after %s: %q", time.Since(start).Round(time.Second), strings.TrimSpace(res.Content))

	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := rt.Sessions().GetMessages(ctx, s.ID, 50)
		for _, m := range msgs {
			if m.Role == "assistant" && strings.Contains(m.MetaJSON, `"wake"`) {
				t.Logf("woken after %s, the agent said: %s", time.Since(start).Round(time.Second), m.Content)
				if _, err := os.Stat(filepath.Join(ws, "done.txt")); err != nil {
					t.Fatalf("woke but the file is not there: %v", err)
				}
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("the finished command did not start a new turn")
}

// The other half: when the agent collects the results itself in the same turn,
// the finished commands must not start a second turn about the same results.
func TestLiveCollectedResultsDoNotWakeAgain(t *testing.T) {
	if os.Getenv("AGENTGO_LIVE_WAKE") != "1" {
		t.Skip("set AGENTGO_LIVE_WAKE=1 to run against the saved model")
	}
	saved, err := os.ReadFile(filepath.Join(DefaultDataDir(), "config.json"))
	if err != nil {
		t.Skip("no saved config.json:", err)
	}
	data, ws := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "config.json"), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	rt, err := newRuntimeAt(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if _, err := rt.SetWorkspaceRoot(ws); err != nil {
		t.Fatal(err)
	}
	if rt.LLMConfig().APIKey == "" {
		t.Skip("saved config has no api key")
	}
	ctx := context.Background()
	s, err := rt.Sessions().Create(ctx, "collect live")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "请用 execute_bash 分别在后台（run_in_background=true）运行两条命令：" +
		"pwsh -NoProfile -Command \"Start-Sleep -Seconds 6; Write-Output one\" 和 pwsh -NoProfile -Command \"Start-Sleep -Seconds 6; Write-Output two\"。" +
		"然后用 task_output（block=true）等它们都结束，最后告诉我两条命令各自输出了什么。"
	res, err := rt.AgentRunner().GenerateStream(ctx, rt.LLMConfig().agentSettings(), s.ID, prompt, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answer: %q", strings.TrimSpace(res.Content))
	if !strings.Contains(res.Content, "one") || !strings.Contains(res.Content, "two") {
		t.Skip("the model did not collect both results in this turn; nothing to check")
	}
	time.Sleep(8 * time.Second) // debounce plus the time a stray wake would need to land
	msgs, _ := rt.Sessions().GetMessages(ctx, s.ID, 50)
	for _, m := range msgs {
		if strings.Contains(m.MetaJSON, `"wake"`) {
			t.Fatalf("a result the agent had already read woke it again: %s", m.Content)
		}
	}
}
