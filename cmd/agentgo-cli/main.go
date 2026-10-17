// Command agentgo-cli is AgentGo's own terminal client. It runs the AgentGo
// brain in-process; it needs no Crush binary. An interactive chat on a terminal
// is the full-screen TUI (internal/tui); -plain, pipes and `run` use the line
// mode (internal/cli).
package main

import (
	"context"
	"fmt"
	"os"

	"agentgo/internal/cli"
	"agentgo/internal/tui"
)

func main() {
	env := cli.StdIO()
	env.TUI = runTUI
	os.Exit(cli.Main(os.Args[1:], env, openStandalone))
}

func runTUI(be cli.Backend, o cli.TUIOptions) int {
	err := tui.Run(context.Background(), be, tui.Options{
		Workspace: o.Workspace, Session: o.Session, Yolo: o.Yolo, Thinking: o.Thinking, Version: cli.Version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentgo-cli:", err)
		return cli.ExitError
	}
	return cli.ExitOK
}
