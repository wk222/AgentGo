// Command agentgo-re is AgentGo's own reverse-engineering MCP server (stdio).
//
//	agentgo-re [--home DIR] [--allow-dynamic]
//
// Register it in agentgo (mcp_servers.json), Cursor, Claude Code, or any MCP client.
package main

import (
	"flag"
	"fmt"
	"os"

	"agentgo/internal/config"
	"agentgo/internal/rekit"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	home := flag.String("home", "", "case/ledger/spill directory (default $AGENTGO_RE_HOME or ./.agentgo-re)")
	dyn := flag.Bool("allow-dynamic", config.ReAllowDynamic.On(), "allow dynamic tools (adb, frida)")
	ver := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *ver {
		fmt.Println("agentgo-re", rekit.Version)
		return
	}
	h := rekit.Home(*home)
	if err := os.MkdirAll(h, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "agentgo-re: cannot create home:", err)
		os.Exit(1)
	}
	if err := server.ServeStdio(rekit.NewServer(h, *dyn)); err != nil {
		fmt.Fprintln(os.Stderr, "agentgo-re:", err)
		os.Exit(1)
	}
}
