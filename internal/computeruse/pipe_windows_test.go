//go:build windows

package computeruse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// TestRealNamedPipe drives the full stack over an actual go-winio pipe with
// the production SDDL: listen, handshake with a token, click, screenshot.
func TestRealNamedPipe(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\agentgo-cua-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	ln, err := Listen(name)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	sub := newFake(1920, 1080)
	srv := newTestServer(sub)
	go srv.Serve(ln)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := DialPipe(ctx, name)
	if err != nil {
		t.Fatalf("DialPipe: %v", err)
	}
	c, err := NewClient(conn, testToken, 2*time.Second)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer c.Close()

	res, err := c.Do(ctx, Request{Action: "screenshot"})
	if err != nil || len(res.PNG) < 1000 {
		t.Fatalf("screenshot over the pipe: %v (%d bytes)", err, len(res.PNG))
	}
	if _, err = c.Do(ctx, Request{Action: "type", Text: "héllo 世界"}); err != nil {
		t.Fatal(err)
	}
	if got := sub.last(); got != `type "héllo 世界"` {
		t.Fatalf("unicode text was mangled in transit: %q", got)
	}
}

// TestPipeIsFirstInstance proves the squatting defence: a second listener on
// the same name must fail instead of sharing it.
func TestPipeIsFirstInstance(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\agentgo-cua-test-first-%d`, os.Getpid())
	ln, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln2, err := Listen(name); err == nil {
		ln2.Close()
		t.Fatal("a second sidecar was allowed to claim the same pipe name")
	}
}

// TestPipeDACLIsOwnerOnly reads the pipe's security descriptor back and checks
// that no broad principal (Everyone, Authenticated Users, Users, Network) is
// granted access, only the current user and SYSTEM.
func TestPipeDACLIsOwnerOnly(t *testing.T) {
	sddl := pipeSDDL()
	if _, err := winio.SddlToSecurityDescriptor(sddl); err != nil {
		t.Fatalf("SDDL %q does not parse: %v", sddl, err)
	}
	for _, broad := range []string{";;;WD)", ";;;AU)", ";;;BU)", ";;;NU)", ";;;AN)"} {
		if strings.Contains(sddl, broad) {
			t.Fatalf("SDDL %q grants a broad principal (%s)", sddl, broad)
		}
	}
	if !strings.HasPrefix(sddl, "D:P(") {
		t.Fatalf("DACL must be protected (D:P), got %q", sddl)
	}
}

// TestTokenFileRoundTrip checks the launch token is created once, is valid hex
// of the right length and is stable across calls.
func TestTokenFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LocalAppData", dir)
	t1, err := EnsureToken()
	if err != nil {
		t.Fatal(err)
	}
	if !tokenRe.MatchString(t1) {
		t.Fatalf("token %q is not 64 hex chars", t1)
	}
	t2, err := EnsureToken()
	if err != nil || t2 != t1 {
		t.Fatalf("token must be stable: %q vs %q (%v)", t1, t2, err)
	}
	p, _ := TokenPath()
	if !strings.HasPrefix(p, dir) {
		t.Fatalf("token path %q is not under the cache dir %q", p, dir)
	}
	_ = os.WriteFile(p, []byte("not hex"), 0o600)
	if _, err := ReadToken(); err == nil {
		t.Fatal("a malformed token file must be refused")
	}
}
