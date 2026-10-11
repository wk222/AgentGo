// Command agentgo-cli is AgentGo's own terminal client. It runs the AgentGo
// brain in-process; it needs no Crush binary.
package main

import (
	"os"

	"agentgo/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.StdIO(), openStandalone))
}
