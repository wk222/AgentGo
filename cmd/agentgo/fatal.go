package main

import (
	"log"
	"os"
	"path/filepath"

	"agentgo/internal/bridge"
)

// interactiveStart reports whether a person is waiting for the window (the default desktop mode).
// The resident gateway (--serve) and the ACP stdio server (--acp) are started by other programs, which a
// modal dialog would leave hanging.
func interactiveStart() bool {
	return !hasArg("--serve") && !hasArg("--acp") && !hasArg("acp")
}

// fatalStart ends the process after a failure to start, and says why. The desktop build has no
// console, so a plain log.Fatal looked like the program silently not opening (a database from a newer
// version, a locked data folder, a missing WebView2 runtime ...).
func fatalStart(what string, err error) {
	text := what + "：\n\n" + err.Error() +
		"\n\n详细日志：" + filepath.Join(bridge.DefaultDataDir(), "agentgo.log")
	log.Print("[AgentGo] " + text)
	if interactiveStart() {
		showFatalDialog("AgentGo 无法启动", text)
	}
	os.Exit(1)
}
