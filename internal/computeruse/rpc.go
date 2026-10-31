package computeruse

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Wire protocol (newline-delimited JSON, one connection = one client):
//
//	client -> {"cua":"hello","version":1,"token":"<hex>"}
//	server -> {"cua":"welcome","version":1}            (or {"cua":"rejected","error":"..."})
//	client -> {"id":1,"method":"computer_use","arguments":{<Request>},"deadlineEpochMs":N}
//	server -> {"id":1,"ok":true,"result":{<Result>}}   (or "ok":false,"error":{<Error>})
//
// It mirrors the framing of Cursor's sidecar so the same mental model applies.
const (
	ProtocolVersion  = 1
	methodComputer   = "computer_use"
	deadlineGraceMs  = 2000
	defaultHandshake = 5 * time.Second
)

type hello struct {
	CUA     string `json:"cua"`
	Version int    `json:"version"`
	Token   string `json:"token"`
}

type welcome struct {
	CUA     string `json:"cua"`
	Version int    `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type rpcRequest struct {
	ID              uint64  `json:"id"`
	Method          string  `json:"method"`
	Arguments       Request `json:"arguments"`
	DeadlineEpochMs int64   `json:"deadlineEpochMs,omitempty"`
}

type rpcResponse struct {
	ID     uint64  `json:"id"`
	OK     bool    `json:"ok"`
	Result *Result `json:"result,omitempty"`
	Error  *Error  `json:"error,omitempty"`
}

// Server exposes a Doer (normally a *Controller) to authenticated clients.
type Server struct {
	Do Doer
	// Token returns the expected launch token. It is called on every
	// connection so a rotated token takes effect without a restart.
	Token            func() (string, error)
	HandshakeTimeout time.Duration
	Logf             func(format string, args ...any)

	conns     atomic.Int32
	lastClose atomic.Int64 // unix nanos of the last disconnect
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// IdleFor reports how long the server has had no client (0 while one is connected).
func (s *Server) IdleFor(start time.Time) time.Duration {
	if s.conns.Load() > 0 {
		return 0
	}
	last := time.Unix(0, s.lastClose.Load())
	if s.lastClose.Load() == 0 || last.Before(start) {
		last = start
	}
	return time.Since(last)
}

// Serve accepts connections until ln is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	s.conns.Add(1)
	defer func() {
		conn.Close()
		s.lastClose.Store(time.Now().UnixNano())
		s.conns.Add(-1)
	}()
	timeout := s.HandshakeTimeout
	if timeout <= 0 {
		timeout = defaultHandshake
	}
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var h hello
	if err := dec.Decode(&h); err != nil {
		s.logf("client sent no valid handshake within %s: %v; dropping the connection", timeout, err)
		return
	}
	reject := func(msg string) {
		s.logf("rejected client handshake: %s", msg)
		_ = enc.Encode(welcome{CUA: "rejected", Error: msg})
	}
	if h.CUA != "hello" {
		reject("first line was not a handshake frame (a client must authenticate before any RPC)")
		return
	}
	if h.Version != ProtocolVersion {
		reject(fmt.Sprintf("handshake version %d is not supported (server speaks %d)", h.Version, ProtocolVersion))
		return
	}
	want, err := s.Token()
	if err != nil || want == "" {
		reject("server has no launch token")
		return
	}
	if subtle.ConstantTimeCompare([]byte(h.Token), []byte(want)) != 1 {
		reject("presented token did not match")
		return
	}
	if err := enc.Encode(welcome{CUA: "welcome", Version: ProtocolVersion}); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	for {
		var rq rpcRequest
		if err := dec.Decode(&rq); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.logf("client read: %v", err)
			}
			return
		}
		resp := s.dispatch(rq)
		if err := enc.Encode(resp); err != nil {
			s.logf("client write: %v", err)
			return
		}
	}
}

func (s *Server) dispatch(rq rpcRequest) rpcResponse {
	resp := rpcResponse{ID: rq.ID}
	if rq.Method != methodComputer {
		resp.Error = NewError(CodeUnsupported, "unknown method %q", rq.Method)
		return resp
	}
	ctx := context.Background()
	if rq.DeadlineEpochMs > 0 {
		dl := time.UnixMilli(rq.DeadlineEpochMs)
		if !time.Now().Before(dl) {
			resp.Error = NewError(CodeTimeout, "the deadline passed before the tool ran, so nothing was actuated; calling again is safe")
			return resp
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, dl)
		defer cancel()
	}
	res, err := s.Do.Do(ctx, rq.Arguments)
	if err != nil {
		resp.Error = AsError(err)
		return resp
	}
	resp.OK, resp.Result = true, res
	return resp
}

// Client is a Doer backed by a sidecar connection. One request is in flight at
// a time; if a response is ever lost the connection is dropped, because later
// replies could no longer be matched to requests.
type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	dec    *json.Decoder
	enc    *json.Encoder
	next   uint64
	broken bool
}

// NewClient authenticates over conn and returns a ready Client.
func NewClient(conn net.Conn, token string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = defaultHandshake
	}
	c := &Client{conn: conn, dec: json.NewDecoder(conn), enc: json.NewEncoder(conn)}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})
	if err := c.enc.Encode(hello{CUA: "hello", Version: ProtocolVersion, Token: token}); err != nil {
		conn.Close()
		return nil, NewError(CodeSidecarUnavailable, "handshake write: %v", err)
	}
	var w welcome
	if err := c.dec.Decode(&w); err != nil {
		conn.Close()
		return nil, NewError(CodeSidecarUnavailable, "handshake read failed or timed out: %v", err)
	}
	if w.CUA != "welcome" {
		conn.Close()
		return nil, NewError(CodeSidecarUnavailable, "handshake rejected: %s", w.Error)
	}
	return c, nil
}

// Close drops the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broken = true
	return c.conn.Close()
}

// Do sends req and waits for the reply.
func (c *Client) Do(ctx context.Context, req Request) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, NewError(CodeSidecarUnavailable, "the computer-use sidecar connection is closed; the user has to restart it")
	}
	c.next++
	rq := rpcRequest{ID: c.next, Method: methodComputer, Arguments: req}
	if dl, ok := ctx.Deadline(); ok {
		rq.DeadlineEpochMs = dl.UnixMilli()
		_ = c.conn.SetDeadline(dl.Add(deadlineGraceMs * time.Millisecond))
	} else {
		_ = c.conn.SetDeadline(time.Time{})
	}
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetDeadline(time.Now()) })
	defer stop()

	if err := c.enc.Encode(rq); err != nil {
		c.broken = true
		return nil, NewError(CodeSidecarUnavailable, "the computer-use helper could not be reached: %v", err)
	}
	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		c.broken = true
		c.conn.Close()
		return nil, NewError(CodeOutcomeUnknown,
			"the request was delivered but no answer came back (%v), so the action may or may not have happened; take a screenshot before acting again, because a blind retry could double a click or keystroke", err)
	}
	if resp.ID != rq.ID {
		c.broken = true
		c.conn.Close()
		return nil, NewError(CodeOutcomeUnknown, "reply id %d does not match request %d; the connection was dropped", resp.ID, rq.ID)
	}
	if !resp.OK {
		if resp.Error == nil {
			return nil, NewError(CodeInputFailed, "the sidecar failed without a reason")
		}
		return nil, resp.Error
	}
	if resp.Result == nil {
		return nil, NewError(CodeInputFailed, "the sidecar returned neither a result nor an error")
	}
	return resp.Result, nil
}
