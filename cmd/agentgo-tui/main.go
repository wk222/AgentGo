// Command agentgo-tui is the terminal body of AgentGo: the stock Crush TUI.
//
//	go build -o agentgo-tui.exe ./cmd/agentgo-tui
//	agentgo-tui -workspace D:\proj [-yolo] [-session <id>] [-continue]
//
// It runs in one of two ways:
//
//   - attached: the desktop app (or agentgo-headless) is running and published
//     itself as the host for this data directory. The TUI becomes a client of
//     that process, so its runs live there — the desktop UI can see and approve
//     them, and the TUI shows the outcome. Model settings are the host's.
//   - standalone: no host is running. The AgentGo brain and the TUI share this
//     one process over an in-memory pipe (no port, no socket, no child process).
//     LLM settings come from AgentGo's saved config; if AZURE_OPENAI_API_KEY and
//     AZURE_OPENAI_API_ENDPOINT are set they override it for this process.
//
// By default it attaches when it can. -attach requires a host (and fails if
// there is none); -standalone never attaches.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"agentgo/internal/bridge"
	"agentgo/internal/hostlink"

	"github.com/charmbracelet/crush/pkg/embedtui"
)

func main() {
	ws := flag.String("workspace", "", "workspace root (default: current directory)")
	yolo := flag.Bool("yolo", false, "auto-grant permission prompts")
	engineID := flag.String("engine", "", "engine id (default: router default); standalone only")
	sessionID := flag.String("session", "", "resume this session id")
	cont := flag.Bool("continue", false, "resume the most recent session")
	debug := flag.Bool("debug", false, "Crush debug logging (to its log file)")
	api := flag.String("api", "", `LLM wire API: "responses" streams reasoning summaries (Azure gpt-5/gpt-6, o-series); default chat/completions; standalone only`)
	attach := flag.Bool("attach", false, "attach to the running AgentGo host; fail if there is none")
	standalone := flag.Bool("standalone", false, "run on our own, even if a host is running")
	flag.Parse()
	if *attach && *standalone {
		log.Fatal("-attach and -standalone exclude each other")
	}

	root := *ws
	if root == "" {
		root, _ = os.Getwd()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := embedtui.Options{
		Cwd:          root,
		Debug:        *debug,
		YOLO:         *yolo,
		SessionID:    *sessionID,
		ContinueLast: *cont,
	}

	var err error
	if info, ok := findHost(ctx, *standalone, *attach); ok {
		for name, set := range map[string]bool{"-engine": *engineID != "", "-api": *api != ""} {
			if set {
				fmt.Fprintf(os.Stderr, "agentgo-tui: %s is ignored when attached: the host decides\n", name)
			}
		}
		err = runAttached(ctx, info, opts)
	} else {
		err = runStandalone(ctx, root, *yolo, *engineID, *api, opts)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentgo-tui:", err)
		os.Exit(1)
	}
}

// findHost looks for a live host unless told not to. With -attach the absence
// of one is an error; otherwise it only means "run standalone".
func findHost(ctx context.Context, standalone, mustAttach bool) (hostlink.Info, bool) {
	if standalone {
		return hostlink.Info{}, false
	}
	info, err := hostlink.Discover(ctx, bridge.DefaultDataDir())
	if err == nil {
		return info, true
	}
	if mustAttach {
		log.Fatalf("no AgentGo host is running for %s (start the desktop app or agentgo-headless, or drop -attach)", bridge.DefaultDataDir())
	}
	return hostlink.Info{}, false
}

// runAttached uses the host's brain over its authenticated endpoint. To the
// TUI the host looks like the in-process server it normally gets.
func runAttached(ctx context.Context, info hostlink.Info, opts embedtui.Options) error {
	fmt.Fprintf(os.Stderr, "agentgo-tui: attached to host pid %d (%s)\n", info.PID, info.Addr)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lost := hostlink.Monitor(ctx, info, 2*time.Second, 3)
	var hostGone atomic.Bool
	go func() {
		select {
		case <-lost:
			hostGone.Store(true)
			cancel() // ends the TUI instead of leaving it on a dead stream
		case <-ctx.Done():
		}
	}()

	var handler http.Handler = hostlink.Proxy(info)
	err := embedtui.Run(ctx, handler, opts)
	if hostGone.Load() {
		return errors.New("the AgentGo host stopped; your sessions are kept — start it again and run with -continue")
	}
	return err
}

// runStandalone is the single-process mode: brain and TUI together.
func runStandalone(ctx context.Context, root string, yolo bool, engineID, api string, opts embedtui.Options) error {
	if api != "" {
		os.Setenv("AGENTGO_LLM_API", api)
	}
	if yolo {
		os.Setenv("AGENTGO_CRUSHPROTO_YOLO", "1")
	}
	if engineID != "" {
		os.Setenv("AGENTGO_CRUSHPROTO_ENGINE", engineID)
	}
	if os.Getenv("AGENTGO_DISABLE_SCHEDULER") == "" {
		os.Setenv("AGENTGO_DISABLE_SCHEDULER", "1")
	}
	// This process owns its run in-process; it is not what others attach to.
	if os.Getenv("AGENTGO_HOSTLINK") == "" {
		os.Setenv("AGENTGO_HOSTLINK", "0")
	}

	rt, err := bridge.NewRuntime()
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	if _, err := rt.SetWorkspaceRoot(root); err != nil {
		return fmt.Errorf("workspace %s: %w", root, err)
	}
	if key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT"); key != "" && ep != "" {
		model := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
		if model == "" {
			model = "gpt-6-luna"
		}
		if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
			// chat/completions rejects function tools on reasoning models unless
			// effort is "none"; the Responses API has no such limit.
			if strings.EqualFold(os.Getenv("AGENTGO_LLM_API"), "responses") {
				os.Setenv("AGENTGO_REASONING_EFFORT", "medium")
			} else {
				os.Setenv("AGENTGO_REASONING_EFFORT", "none")
			}
		}
		cfg := bridge.LLMConfig{APIBase: strings.TrimRight(ep, "/") + "/openai/v1/", APIKey: key, Model: model}
		if err := rt.SetLLMConfig(cfg); err != nil {
			return fmt.Errorf("llm config: %w", err)
		}
	}

	svc := bridge.NewAppService(rt)
	defer svc.Close()

	handler, closeBrain, err := svc.CrushHandler()
	if err != nil {
		return fmt.Errorf("brain: %w", err)
	}
	defer closeBrain()

	return embedtui.Run(ctx, handler, opts)
}
