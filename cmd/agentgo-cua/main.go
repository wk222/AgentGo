// agentgo-cua is the computer-use sidecar: it owns the screen capture and input
// injection, and serves them over an owner-only named pipe (go-winio).
//
// Why a separate process at all: it can be started elevated so the agent can
// drive administrator windows that Windows (UIPI) hides from a normal process,
// and a crash in capture/input code cannot take the desktop app down.
//
// The launcher must write the launch token first (computeruse.EnsureToken);
// the sidecar refuses to listen without one. For a console-less sidecar build
// with: go build -ldflags "-H=windowsgui" ./cmd/agentgo-cua
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"agentgo/internal/computeruse"
)

func main() {
	pipe := flag.String("pipe", computeruse.PipeName(), "named pipe to listen on")
	idle := flag.Duration("idle", 2*time.Minute, "exit after this long with no client (0 = never)")
	noHotkey := flag.Bool("no-hotkey", false, "do not register the global stop hotkey")
	flag.Parse()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "agentgo-cua: "+format+"\n", args...)
	}
	if !computeruse.Supported() {
		logf("%v", computeruse.ErrUnsupported)
		os.Exit(1)
	}
	if _, err := computeruse.ReadToken(); err != nil {
		logf("refusing to listen without a launch binding: %v\n  the launching process must write the launch token before starting the sidecar", err)
		os.Exit(1)
	}

	ctl := computeruse.New(computeruse.NewSubstrate(), computeruse.WithAccessibility(computeruse.NewAccessibility()))
	if !*noHotkey {
		stop, err := computeruse.StartStopHotkey(ctl)
		if err != nil {
			logf("stop hotkey disabled: %v", err)
		} else {
			defer stop()
			logf("%s stops the session", computeruse.StopHotkeyLabel)
		}
	}

	ln, err := computeruse.Listen(*pipe)
	if err != nil {
		logf("failed to create pipe %s: %v", *pipe, err)
		os.Exit(1)
	}
	srv := &computeruse.Server{Do: ctl, Token: computeruse.ReadToken, Logf: logf}
	logf("listening on %s (owner-only DACL, first instance, token required on every connection)", *pipe)

	start := time.Now()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		ln.Close()
	}()
	if *idle > 0 {
		go func() {
			for range time.Tick(5 * time.Second) {
				if srv.IdleFor(start) >= *idle {
					logf("idle for %s with no client; exiting", *idle)
					ln.Close()
					return
				}
			}
		}()
	}
	if err := srv.Serve(ln); err != nil {
		logf("serve: %v", err)
		os.Exit(1)
	}
}
