package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"agentgo/internal/bridge"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rt, err := bridge.NewRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %v\n", err)
		os.Exit(1)
	}
	cfg := rt.LLMConfig()
	fmt.Printf("data_dir=%s\n", rt.DataDir())
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
