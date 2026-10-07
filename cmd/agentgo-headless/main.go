// Command agentgo-headless runs the AgentGo brain without the desktop UI and
// serves the Crush client/server protocol, so the stock Crush TUI is a body of
// AgentGo (Eino agent, governance, memory, plugin tools).
//
//	agentgo-headless -workspace D:\proj -addr 127.0.0.1:18770
//	$env:CRUSH_CLIENT_SERVER = "1"; crush -H tcp://127.0.0.1:18770
//
// LLM settings come from AgentGo's saved config; if AZURE_OPENAI_API_KEY and
// AZURE_OPENAI_API_ENDPOINT are set they override it for this process.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"agentgo/internal/bridge"
)

func main() {
	ws := flag.String("workspace", "", "workspace root (default: current directory)")
	addr := flag.String("addr", "127.0.0.1:18770", "crush protocol listen address (loopback only)")
	yolo := flag.Bool("yolo", false, "auto-grant permission prompts shown in the TUI")
	engineID := flag.String("engine", "", "engine id (default: router default)")
	flag.Parse()

	os.Setenv("AGENTGO_CRUSHPROTO_ADDR", *addr)
	if *yolo {
		os.Setenv("AGENTGO_CRUSHPROTO_YOLO", "1")
	}
	if *engineID != "" {
		os.Setenv("AGENTGO_CRUSHPROTO_ENGINE", *engineID)
	}
	if os.Getenv("AGENTGO_DISABLE_SCHEDULER") == "" {
		os.Setenv("AGENTGO_DISABLE_SCHEDULER", "1")
	}

	root := *ws
	if root == "" {
		root, _ = os.Getwd()
	}

	rt, err := bridge.NewRuntime()
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	if _, err := rt.SetWorkspaceRoot(root); err != nil {
		log.Fatalf("workspace %s: %v", root, err)
	}
	if key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT"); key != "" && ep != "" {
		model := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
		if model == "" {
			model = "gpt-6-luna"
		}
		if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
			os.Setenv("AGENTGO_REASONING_EFFORT", "none")
		}
		cfg := bridge.LLMConfig{APIBase: strings.TrimRight(ep, "/") + "/openai/v1/", APIKey: key, Model: model}
		if err := rt.SetLLMConfig(cfg); err != nil {
			log.Fatalf("llm config: %v", err)
		}
	}

	svc := bridge.NewAppService(rt)
	defer svc.Close()
	fmt.Printf("agentgo-headless: workspace=%s crush protocol on %s (Ctrl+C to stop)\n", root, *addr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
