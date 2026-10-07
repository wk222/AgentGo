// crushproto-probe serves Crush's /v1 protocol with a scripted agent (no model),
// so the stock Crush client/TUI can be pointed at it:
//
//	go run ./cmd/crushproto-probe -addr 127.0.0.1:18770
//	$env:CRUSH_CLIENT_SERVER = "1"; crush -H tcp://127.0.0.1:18770          # TUI
//	$env:CRUSH_CLIENT_SERVER = "1"; crush run -H tcp://127.0.0.1:18770 hi    # headless
//
// Every request is logged; 501 lines are protocol surface still to implement.
package main

import (
	"flag"
	"log"
	"net/http"

	"agentgo/internal/crushproto"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18770", "listen address (tcp)")
	yolo := flag.Bool("yolo", false, "skip permission prompts")
	flag.Parse()

	srv, err := crushproto.New(crushproto.Options{SkipPermissions: *yolo})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	log.Printf("crushproto-probe listening on tcp://%s (yolo=%v)", *addr, *yolo)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
