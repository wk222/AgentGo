package bridge

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/crushproto"
	"agentgo/internal/plugin"
)

// newCrushprotoServer builds the protocol server over the AppService's brain.
// Shared by the TCP plugin and the in-process embedded terminal; logf may be
// a no-op in the embedded TUI, which owns the terminal.
func newCrushprotoServer(s *AppService, logf func(string, ...any)) (*crushproto.Server, error) {
	return crushproto.New(crushproto.Options{
		Runner:          newCrushRunner(s, strings.TrimSpace(os.Getenv("AGENTGO_CRUSHPROTO_ENGINE"))),
		Logf:            logf,
		SkipPermissions: os.Getenv("AGENTGO_CRUSHPROTO_YOLO") == "1",
		Store:           crushproto.FileStore{Dir: filepath.Join(s.rt.DataDir(), "crushproto")},
		LSPProvider:     &crushprotoLSPBridge{svc: s},
		ModelProvider:   &crushprotoModelBridge{svc: s},
	})
}

// CrushHandler returns the Crush /v1 protocol as an http.Handler for in-process
// use (no TCP port), plus a close func that ends its SSE streams. This is the
// brain half of the embedded terminal body.
func (s *AppService) CrushHandler() (http.Handler, func(), error) {
	if s == nil || s.rt == nil {
		return nil, nil, fmt.Errorf("crush handler: runtime not ready")
	}
	srv, err := newCrushprotoServer(s, func(string, ...any) {})
	if err != nil {
		return nil, nil, err
	}
	return srv.Handler(), srv.Close, nil
}

// crushprotoPlugin serves the Crush client/server protocol so the stock Crush
// TUI can use AgentGo as its backend. Opt-in (it opens a TCP port):
//
//	AGENTGO_CRUSHPROTO_ADDR=127.0.0.1:18770   listen address; unset = disabled
//	AGENTGO_CRUSHPROTO_ENGINE=<id>            engine to use (default: router default)
//	AGENTGO_CRUSHPROTO_YOLO=1                 auto-grant TUI permission prompts
//
// Connect with: CRUSH_CLIENT_SERVER=1 crush -H tcp://127.0.0.1:18770
// Loopback only: the protocol has no authentication.
//
// This adapter drives the whole application (sessions, runs, approvals, LSP and
// model views), so it declares a dependency on the "brain" service, which is the
// AppService. That is the one first-party plugin still coupled to it; the
// dependency is visible in the plugin graph and the architecture tests keep it
// the only one.
func crushprotoPlugin(rt *Runtime) (plugin.Plugin, bool) {
	addr := strings.TrimSpace(os.Getenv("AGENTGO_CRUSHPROTO_ADDR"))
	if rt == nil || addr == "" {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "crushproto",
		Inject: []string{svcEngines, svcBrain},
		Apply: func(c *plugin.Context) error {
			s, err := plugin.Use[*AppService](c, svcBrain)
			if err != nil {
				return err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("AGENTGO_CRUSHPROTO_ADDR: %w", err)
			}
			if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return fmt.Errorf("refusing non-loopback address %q: the protocol is unauthenticated", addr)
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			srv, err := newCrushprotoServer(s, log.Printf)
			if err != nil {
				_ = ln.Close()
				return err
			}
			hs := &http.Server{Handler: srv.Handler()}
			c.Effect("stop crushproto", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				srv.Close() // ends SSE streams so Shutdown can finish
				_ = hs.Shutdown(ctx)
			})
			go func() {
				if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
					log.Printf("[crushproto] serve: %v", err)
				}
			}()
			log.Printf("[crushproto] serving Crush protocol on %s", ln.Addr())
			return nil
		},
	}, true
}
