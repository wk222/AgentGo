package computeruse

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestServer(sub Substrate) *Server {
	return &Server{
		Do:    New(sub, WithSettle(0)),
		Token: func() (string, error) { return testToken, nil },
	}
}

// serveOver runs the server on one end of an in-memory pipe and returns the other.
func serveOver(t *testing.T, s *Server) net.Conn {
	t.Helper()
	srv, cli := net.Pipe()
	go s.handle(srv)
	t.Cleanup(func() { cli.Close() })
	return cli
}

func TestRPCRoundTrip(t *testing.T) {
	sub := newFake(1920, 1080)
	c, err := NewClient(serveOver(t, newTestServer(sub)), testToken, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := c.Do(ctx, Request{Action: "screenshot"})
	if err != nil || len(res.PNG) == 0 {
		t.Fatalf("screenshot: %v", err)
	}
	if _, err = c.Do(ctx, Request{Action: "click", X: fp(728), Y: fp(409)}); err != nil {
		t.Fatal(err)
	}
	if got := sub.last(); !strings.HasPrefix(got, "click left 96") && !strings.HasPrefix(got, "click left 960") {
		t.Fatalf("server did not click through the pipe: %q", got)
	}
	// Typed errors survive the wire with their escalation intact.
	_, err = c.Do(ctx, Request{Action: "click", X: fp(99999), Y: fp(1)})
	if e := AsError(err); e.Code != CodeInvalidArguments || e.Escalation != EscRetry {
		t.Fatalf("remote error lost its shape: %#v", e)
	}
}

func TestRPCRejectsBadToken(t *testing.T) {
	_, err := NewClient(serveOver(t, newTestServer(newFake(800, 600))), strings.Repeat("0", 64), time.Second)
	e := AsError(err)
	if e == nil || e.Code != CodeSidecarUnavailable || !strings.Contains(e.Message, "token did not match") {
		t.Fatalf("got %v", err)
	}
}

func TestRPCRejectsRPCBeforeHandshake(t *testing.T) {
	conn := serveOver(t, newTestServer(newFake(800, 600)))
	// An RPC frame where a handshake must be: the server must drop it.
	_, _ = conn.Write([]byte(`{"id":1,"method":"computer_use","arguments":{"action":"screenshot"}}` + "\n"))
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "rejected") {
		t.Fatalf("expected a rejection, got %q", buf[:n])
	}
}

func TestRPCExpiredDeadlineActuatesNothing(t *testing.T) {
	sub := newFake(800, 600)
	s := newTestServer(sub)
	resp := s.dispatch(rpcRequest{ID: 7, Method: methodComputer, Arguments: Request{Action: "screenshot"}, DeadlineEpochMs: time.Now().Add(-time.Second).UnixMilli()})
	if resp.OK || resp.Error.Code != CodeTimeout || len(sub.calls) != 0 {
		t.Fatalf("got %#v calls=%v", resp, sub.calls)
	}
}

func TestClientLostReplyIsOutcomeUnknown(t *testing.T) {
	srv, cli := net.Pipe()
	go func() { // handshake, then swallow the request and hang up without answering
		s := newTestServer(newFake(800, 600))
		s.handleHandshakeOnly(srv)
	}()
	c, err := NewClient(cli, testToken, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), Request{Action: "screenshot"})
	if e := AsError(err); e.Code != CodeOutcomeUnknown || e.Escalation != EscUseDifferentTool {
		t.Fatalf("got %v", err)
	}
	_, err = c.Do(context.Background(), Request{Action: "screenshot"})
	if AsError(err).Code != CodeSidecarUnavailable {
		t.Fatalf("a dropped connection must stay dropped, got %v", err)
	}
}

// handleHandshakeOnly authenticates, reads one request and closes - a crash
// between "delivered" and "answered".
func (s *Server) handleHandshakeOnly(conn net.Conn) {
	defer conn.Close()
	dec := newDec(conn)
	var h hello
	if dec.Decode(&h) != nil {
		return
	}
	_ = newEnc(conn).Encode(welcome{CUA: "welcome", Version: ProtocolVersion})
	var rq rpcRequest
	_ = dec.Decode(&rq)
}
