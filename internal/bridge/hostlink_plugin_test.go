package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentgo/internal/hostlink"
)

func newHostService(t *testing.T, dir string) *AppService {
	t.Helper()
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", "")
	rt := &Runtime{dataDir: dir, llm: LLMConfig{APIBase: "http://127.0.0.1:11434/v1", Model: "qwen3"}}
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	return s
}

func hostCap(s *AppService, id string) Capability {
	for _, c := range s.Capabilities() {
		if c.ID == id {
			return c
		}
	}
	return Capability{}
}

func TestHostIsDiscoverableAuthenticatedAndServesTheProtocol(t *testing.T) {
	t.Setenv(hostlinkEnv, "")
	dir := t.TempDir()
	s := newHostService(t, dir)
	ctx := context.Background()

	info, err := hostlink.Discover(ctx, dir)
	if err != nil {
		t.Fatalf("a started host must be discoverable: %v", err)
	}
	if !strings.HasPrefix(info.Addr, "127.0.0.1:") {
		t.Fatalf("host must listen on loopback only, got %s", info.Addr)
	}
	if !hostCap(s, "host.attach").Available {
		t.Fatalf("capability not reported: %+v", hostCap(s, "host.attach"))
	}

	// No token, no service.
	resp, err := http.Get(info.URL() + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v1/health = %d, want 401", resp.StatusCode)
	}

	// An attached client sees the protocol the TUI expects, and the host's own
	// description of what it can do.
	front := httptest.NewServer(hostlink.Proxy(info))
	defer front.Close()
	resp, err = http.Get(front.URL + "/v1/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("attached /v1/health: %v %v", resp, err)
	}
	resp.Body.Close()

	resp, err = http.Get(front.URL + "/host/info")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("attached /host/info: %v %v", resp, err)
	}
	defer resp.Body.Close()
	var got struct {
		PID          int          `json:"pid"`
		Capabilities []Capability `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.PID != os.Getpid() || len(got.Capabilities) == 0 {
		t.Fatalf("host info = %+v", got)
	}
}

func TestOnlyOneHostPerDataDirectory(t *testing.T) {
	t.Setenv(hostlinkEnv, "")
	dir := t.TempDir()
	first := newHostService(t, dir)
	before, err := hostlink.Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	second := newHostService(t, dir) // same data directory, same process for the test
	c := hostCap(second, "host.attach")
	if c.Available || !strings.Contains(c.Reason, "another host is running") {
		t.Fatalf("second host must say why it is not attachable: %+v", c)
	}
	after, err := hostlink.Discover(context.Background(), dir)
	if err != nil || after.Token != before.Token {
		t.Fatalf("the second process took over the announcement: %+v %v", after, err)
	}
	if !hostCap(first, "host.attach").Available {
		t.Fatal("the first host must stay attachable")
	}
}

func TestClosingTheHostWithdrawsItsAnnouncement(t *testing.T) {
	t.Setenv(hostlinkEnv, "")
	dir := t.TempDir()
	rt := &Runtime{dataDir: dir}
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", "")
	s := NewAppService(rt)
	info, err := hostlink.Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := hostlink.Discover(context.Background(), dir); !errors.Is(err, hostlink.ErrNoHost) {
		t.Fatalf("a closed host is still discoverable: %v", err)
	}
	if _, err := os.Stat(hostlink.Path(dir)); !os.IsNotExist(err) {
		t.Fatal("the announcement file was left behind")
	}
	// And its port stops answering, so an attached client notices.
	lost := hostlink.Monitor(context.Background(), info, 10*time.Millisecond, 2)
	select {
	case <-lost:
	case <-time.After(5 * time.Second):
		t.Fatal("a client attached to a closed host is never told")
	}
}

func TestHostEndpointCanBeSwitchedOff(t *testing.T) {
	t.Setenv(hostlinkEnv, "0")
	dir := t.TempDir()
	s := newHostService(t, dir)
	if _, err := hostlink.Discover(context.Background(), dir); !errors.Is(err, hostlink.ErrNoHost) {
		t.Fatalf("switched off, yet a host was announced: %v", err)
	}
	c := hostCap(s, "host.attach")
	if c.Available || !strings.Contains(c.Reason, hostlinkEnv) {
		t.Fatalf("reason should tell how to turn it on: %+v", c)
	}
}
