package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"agentgo/internal/bridge"
	"agentgo/internal/config"
)

// llmtest checks an OpenAI-compatible endpoint with AgentGo's own client code.
//
// With AGENTGO_TEST_BASE set it uses AGENTGO_TEST_BASE / AGENTGO_TEST_KEY /
// AGENTGO_TEST_MODEL and never opens the user's data directory (safe while the
// desktop app is running). Without it, it tests the saved configuration.
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var cfg bridge.LLMConfig
	if base := strings.TrimSpace(config.TestLLMBase.Get()); base != "" {
		cfg = bridge.LLMConfig{APIBase: base, APIKey: config.TestLLMKey.Get(), Model: config.TestLLMModel.Get()}
	} else {
		rt, err := bridge.NewRuntime()
		if err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %v\n", err)
			os.Exit(1)
		}
		cfg = rt.LLMConfig()
		fmt.Printf("data_dir=%s\n", rt.DataDir())
	}
	fmt.Printf("api_base=%s model=%s key_set=%v\n", cfg.APIBase, cfg.Model, cfg.APIKey != "")

	test := bridge.TestLLMConnection(ctx, cfg)
	fmt.Printf("TestLLM: ok=%v http=%d msg=%s\n", test.OK, test.HTTPCode, test.Message)
	if !test.OK {
		os.Exit(1)
	}
	for _, id := range []string{"gemini-3.5-flash", cfg.Model} {
		if id == "" {
			continue
		}
		cfg.Model = id
		ans, err := bridge.ChatOnce(ctx, cfg, "", "用一句话中文确认 AgentGo 配置测试成功。")
		if err != nil {
			fmt.Printf("ChatOnce(%s): %v\n", id, err)
			continue
		}
		fmt.Printf("ChatOnce(%s): %s\n", id, ans)
		break
	}
}
