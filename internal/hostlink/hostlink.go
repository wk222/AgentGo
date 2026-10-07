// Package hostlink lets several surfaces share one running AgentGo.
//
// Sharing a data directory is not sharing a run: a run that is paused for
// approval lives in the memory of the process that started it. So the surfaces
// that want to see the same live state must talk to one process — the host —
// and everything else attaches to it as a client. This package is the small
// contract for that: how a host announces itself, how a client finds it, and
// how the connection is authenticated and noticed to be gone.
//
//   - Discovery: the host writes host.json into the data directory (address,
//     one-time token, pid). A client reads it and proves the host is alive by
//     calling /host/ping with the token. A file left by a crashed host fails
//     that call and counts as "no host" — pids are not trusted, they get reused.
//   - Authentication: every request carries "Authorization: Bearer <token>".
//     The listener is loopback-only and the token is fresh per host start.
//   - Disconnect: Monitor reports when the host stops answering, so a client
//     can say so instead of hanging on a dead stream.
//
// The package uses only the standard library.
package hostlink

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the discovery file inside the data directory.
const FileName = "host.json"

// PingPath answers 200 to an authenticated caller.
const PingPath = "/host/ping"

// Info is what a host publishes about itself.
type Info struct {
	PID     int       `json:"pid"`
	Addr    string    `json:"addr"` // host:port on loopback
	Token   string    `json:"token"`
	Started time.Time `json:"started"`
	Version string    `json:"version,omitempty"`
}

// URL is the base URL of the host.
func (i Info) URL() string { return "http://" + i.Addr }

var (
	// ErrNoHost means no live host is published for the data directory.
	ErrNoHost = errors.New("hostlink: no live host")
	// ErrHostRunning means another live host already owns the data directory.
	ErrHostRunning = errors.New("hostlink: another host is running")
)

// Path is the discovery file for a data directory.
func Path(dataDir string) string { return filepath.Join(dataDir, FileName) }

// NewToken returns a fresh unguessable token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("hostlink: no randomness: " + err.Error()) // not recoverable, and never silently weak
	}
	return hex.EncodeToString(b)
}

// Guard admits only requests that carry the token, and only when they were
// addressed to the loopback listener (which blocks DNS-rebinding pages in a
// browser from reaching it even though they run on the same machine).
func Guard(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agentgo-host"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == PingPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Proxy turns a remote host into an http.Handler that adds the token. A client
// that expects to own an engine locally (the Crush TUI) is handed this instead:
// to it, the host looks like the in-process server it already knows.
func Proxy(info Info) http.Handler {
	target, _ := url.Parse(info.URL())
	rp := httputil.NewSingleHostReverseProxy(target)
	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.Header.Set("Authorization", "Bearer "+info.Token)
	}
	rp.FlushInterval = -1 // event streams must not be buffered
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "agentgo host unreachable: "+err.Error(), http.StatusBadGateway)
	}
	return rp
}

// Ping calls the host with its token. Nil means a live host accepted us.
func Ping(ctx context.Context, info Info) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL()+PingPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host answered %s", resp.Status)
	}
	return nil
}

func read(dataDir string) (Info, error) {
	b, err := os.ReadFile(Path(dataDir))
	if err != nil {
		return Info{}, err
	}
	var in Info
	if err := json.Unmarshal(b, &in); err != nil {
		return Info{}, err
	}
	if in.Addr == "" || in.Token == "" {
		return Info{}, errors.New("hostlink: incomplete host file")
	}
	return in, nil
}

// Discover finds the live host of a data directory. A file that does not lead
// to a host that accepts its token is stale and reported as ErrNoHost.
func Discover(ctx context.Context, dataDir string) (Info, error) {
	in, err := read(dataDir)
	if err != nil {
		return Info{}, ErrNoHost
	}
	if err := Ping(ctx, in); err != nil {
		return Info{}, ErrNoHost
	}
	return in, nil
}

// Publish claims the data directory for this host and writes the discovery
// file. It fails with ErrHostRunning if a live host already owns it. release
// removes the file, but only if it still names this host.
func Publish(ctx context.Context, dataDir string, info Info) (release func(), err error) {
	if info.Token == "" || info.Addr == "" {
		return nil, errors.New("hostlink: Publish needs an address and a token")
	}
	if info.PID == 0 {
		info.PID = os.Getpid()
	}
	if info.Started.IsZero() {
		info.Started = time.Now()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := Path(dataDir)
	data, _ := json.MarshalIndent(info, "", "  ")
	for attempt := 0; ; attempt++ {
		// O_EXCL: of two hosts starting together exactly one creates the file.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := f.Write(data)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(path)
				return nil, werr
			}
			return func() {
				if cur, err := read(dataDir); err == nil && cur.Token == info.Token {
					_ = os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) || attempt > 0 {
			return nil, err
		}
		if cur, derr := Discover(ctx, dataDir); derr == nil {
			return nil, fmt.Errorf("%w (pid %d at %s)", ErrHostRunning, cur.PID, cur.Addr)
		}
		_ = os.Remove(path) // stale: its host is gone
	}
}

// Monitor reports (by closing the returned channel) that the host stopped
// answering for `misses` checks in a row. It ends with ctx.
func Monitor(ctx context.Context, info Info, every time.Duration, misses int) <-chan struct{} {
	lost := make(chan struct{})
	go func() {
		missed := 0
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if Ping(ctx, info) == nil {
					missed = 0
					continue
				}
				if ctx.Err() != nil {
					return // cancelled mid-check: that is not the host's fault
				}
				if missed++; missed >= misses {
					close(lost)
					return
				}
			}
		}
	}()
	return lost
}
