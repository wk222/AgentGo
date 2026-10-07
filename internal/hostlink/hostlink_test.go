package hostlink

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// liveHost starts a guarded host and returns its Info.
func liveHost(t *testing.T, inner http.Handler) (Info, *httptest.Server) {
	t.Helper()
	token := NewToken()
	ts := httptest.NewServer(Guard(token, inner))
	t.Cleanup(ts.Close)
	return Info{Addr: strings.TrimPrefix(ts.URL, "http://"), Token: token}, ts
}

func TestGuardRequiresTheToken(t *testing.T) {
	info, ts := liveHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secret")) }))

	for name, auth := range map[string]string{
		"none":         "",
		"wrong":        "Bearer nope",
		"no scheme":    info.Token,
		"basic scheme": "Basic " + info.Token,
	} {
		req, _ := http.NewRequest("GET", ts.URL+"/v1/x", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}

	req, _ := http.NewRequest("GET", ts.URL+"/v1/x", nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token: status %d", resp.StatusCode)
	}
}

func TestGuardRefusesNonLoopbackHostHeader(t *testing.T) {
	info, ts := liveHost(t, http.NotFoundHandler())
	req, _ := http.NewRequest("GET", ts.URL+"/v1/x", nil)
	req.Host = "evil.example.com"
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403 (DNS rebinding)", resp.StatusCode)
	}
}

func TestProxyAddsTheTokenAndStreams(t *testing.T) {
	info, _ := liveHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: one\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	front := httptest.NewServer(Proxy(info)) // the client's handler: no token of its own
	defer front.Close()

	resp, err := http.Get(front.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: proxy did not authenticate", resp.StatusCode)
	}
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case l := <-got:
		if l != "data: one" {
			t.Fatalf("frame %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the event arrived late: the proxy buffers streams")
	}
}

func TestProxyReportsAnUnreachableHost(t *testing.T) {
	info, ts := liveHost(t, http.NotFoundHandler())
	ts.Close()
	front := httptest.NewServer(Proxy(info))
	defer front.Close()
	resp, err := http.Get(front.URL + "/v1/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", resp.StatusCode)
	}
}

func TestDiscoverFindsALiveHostAndIgnoresStaleFiles(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := Discover(ctx, dir); !errors.Is(err, ErrNoHost) {
		t.Fatalf("empty dir: %v", err)
	}

	info, ts := liveHost(t, http.NotFoundHandler())
	release, err := Publish(ctx, dir, info)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Discover(ctx, dir)
	if err != nil || got.Addr != info.Addr || got.PID != os.Getpid() {
		t.Fatalf("discover = %+v, %v", got, err)
	}

	ts.Close() // the host dies and leaves its file behind
	if _, err := Discover(ctx, dir); !errors.Is(err, ErrNoHost) {
		t.Fatalf("stale file must not count as a host: %v", err)
	}
	release()
}

func TestAReusedPortWithAnotherTokenIsNotOurHost(t *testing.T) {
	dir := t.TempDir()
	info, _ := liveHost(t, http.NotFoundHandler())
	wrong := info
	wrong.Token = NewToken() // something else now listens where the file says
	if _, err := Publish(context.Background(), dir, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), dir); !errors.Is(err, ErrNoHost) {
		t.Fatalf("a listener that refuses our token is not our host: %v", err)
	}
}

func TestPublishRefusesALiveHostButReplacesAStaleOne(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first, ts := liveHost(t, http.NotFoundHandler())
	release, err := Publish(ctx, dir, first)
	if err != nil {
		t.Fatal(err)
	}

	second, _ := liveHost(t, http.NotFoundHandler())
	if _, err := Publish(ctx, dir, second); !errors.Is(err, ErrHostRunning) {
		t.Fatalf("second host: %v, want ErrHostRunning", err)
	}
	if got, _ := Discover(ctx, dir); got.Token != first.Token {
		t.Fatal("a refused host must not overwrite the live one")
	}

	ts.Close() // first dies without cleaning up
	rel2, err := Publish(ctx, dir, second)
	if err != nil {
		t.Fatalf("stale file must be replaced: %v", err)
	}
	release() // the old host's late cleanup must not remove the new host's file
	if got, err := Discover(ctx, dir); err != nil || got.Token != second.Token {
		t.Fatalf("late release removed someone else's file: %+v %v", got, err)
	}
	rel2()
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatal("release should remove its own file")
	}
}

func TestMonitorNoticesALostHost(t *testing.T) {
	info, ts := liveHost(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lost := Monitor(ctx, info, 20*time.Millisecond, 3)

	select {
	case <-lost:
		t.Fatal("reported lost while the host was up")
	case <-time.After(150 * time.Millisecond):
	}
	ts.Close()
	select {
	case <-lost:
	case <-time.After(5 * time.Second):
		t.Fatal("never noticed the host was gone")
	}
}

func TestMonitorStaysSilentWhenCancelled(t *testing.T) {
	info, _ := liveHost(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	lost := Monitor(ctx, info, 10*time.Millisecond, 1)
	cancel()
	select {
	case <-lost:
		t.Fatal("cancellation is not a lost host")
	case <-time.After(200 * time.Millisecond):
	}
}
