package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"agentgo/internal/hostlink"
	"agentgo/internal/plugin"
)

// hostlinkEnv switches the host endpoint off ("0"/"false"/"off"). It is on by
// default for the desktop app and the headless server, which are what other
// surfaces attach to. A standalone terminal sets it to off for itself: it owns
// its run in-process and should not become the thing others discover.
const hostlinkEnv = "AGENTGO_HOSTLINK"

func hostlinkEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(hostlinkEnv))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// hostlinkPlugin makes this process the host other surfaces attach to: a
// loopback endpoint, protected by a per-start token and announced in the data
// directory (internal/hostlink), that serves the Crush protocol and a small
// host description. A TUI attached here starts its runs in this process, so the
// desktop UI sees and can approve them, and the TUI sees the outcome.
//
// Only one live host exists per data directory; a second process fails this
// plugin with a message naming the first, and keeps running as an ordinary
// (not attachable) process.
//
// Like crushprotoPlugin it drives the whole application, hence the dependency
// on the "brain" service.
func hostlinkPlugin(rt *Runtime) (plugin.Plugin, bool) {
	if rt == nil || !hostlinkEnabled() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "host",
		Inject: []string{svcEngines, svcBrain},
		Apply: func(c *plugin.Context) error {
			s, err := plugin.Use[*AppService](c, svcBrain)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			logf := func(string, ...any) {} // the stream of protocol requests is noise in a GUI log
			if os.Getenv("AGENTGO_HOSTLINK_LOG") == "1" {
				logf = log.Printf
			}
			crush, err := newCrushprotoServer(s, logf)
			if err != nil {
				_ = ln.Close()
				return err
			}

			token := hostlink.NewToken()
			started := time.Now()
			mux := http.NewServeMux()
			mux.HandleFunc("/host/info", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"pid":          os.Getpid(),
					"started":      started,
					"data_dir":     rt.DataDir(),
					"capabilities": s.Capabilities(),
				})
			})
			mux.Handle("/", crush.Handler())
			hs := &http.Server{Handler: hostlink.Guard(token, mux)}

			// Claim the directory only once the listener exists, so what is
			// published can be reached.
			pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			release, err := hostlink.Publish(pubCtx, rt.DataDir(), hostlink.Info{Addr: ln.Addr().String(), Token: token, Started: started})
			cancel()
			if err != nil {
				_ = ln.Close()
				crush.Close()
				if errors.Is(err, hostlink.ErrHostRunning) {
					return fmt.Errorf("this process is not attachable: %w", err)
				}
				return err
			}

			// Effects run in reverse: withdraw the announcement first, so nobody
			// discovers a host that is shutting down, then stop serving.
			c.Effect("stop host endpoint", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				crush.Close() // ends SSE streams so Shutdown can finish
				_ = hs.Shutdown(ctx)
			})
			c.Effect("withdraw host announcement", release)
			go func() {
				if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("[host] serve: %v", err)
				}
			}()
			log.Printf("[host] attachable on %s (token in %s)", ln.Addr(), hostlink.Path(rt.DataDir()))
			return nil
		},
	}, true
}
