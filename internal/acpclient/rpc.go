package acpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

// maxLine bounds one JSON-RPC message; an agent that streams a whole file in one frame still fits.
const maxLine = 32 << 20

// RPCError is a JSON-RPC error object as the agent sent it.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("agent error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("agent error %d: %s", e.Code, e.Message)
}

// message is any JSON-RPC 2.0 frame: a request (method+id), a notification (method) or a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// rpcConn speaks newline-delimited JSON-RPC over a pair of streams, the transport ACP agents use
// on stdio. Notifications are handled in arrival order on the reader goroutine, so a handler must
// not block; requests from the agent (permission prompts) run on their own goroutines.
type rpcConn struct {
	out io.Writer
	wmu sync.Mutex

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan response
	closed  bool
	exitErr error

	onNotify  func(method string, params json.RawMessage)
	onRequest func(method string, params json.RawMessage) (any, *RPCError)
	onJunk    func(line []byte) // a line that is not JSON-RPC (a banner the agent printed on stdout)

	done chan struct{}
}

func newRPC(in io.Reader, out io.Writer) *rpcConn {
	c := &rpcConn{out: out, pending: map[int64]chan response{}, done: make(chan struct{})}
	go c.readLoop(in)
	return c
}

func (c *rpcConn) readLoop(in io.Reader) {
	r := bufio.NewReaderSize(in, 256<<10)
	var readErr error
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			readErr = err
			break
		}
	}
	// The end of the stream, or our own Close having shut the pipe, is the agent going away; only a
	// real fault (an oversized frame) is worth reporting as such.
	if errors.Is(readErr, io.EOF) || errors.Is(readErr, os.ErrClosed) || errors.Is(readErr, io.ErrClosedPipe) {
		readErr = nil
	}
	c.shutdown(readErr)
}

// readLine reads one frame, refusing a line longer than maxLine instead of growing without end.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			return buf, err
		}
		buf = append(buf, part...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("agent sent a message longer than %d MB", maxLine>>20)
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

func (c *rpcConn) dispatch(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil || (m.Method == "" && len(m.ID) == 0) {
		if c.onJunk != nil {
			c.onJunk(line)
		}
		return
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go c.serve(m)
	case m.Method != "":
		if c.onNotify != nil {
			c.onNotify(m.Method, m.Params)
		}
	default:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if m.Error != nil {
			ch <- response{err: m.Error}
		} else {
			ch <- response{result: m.Result}
		}
	}
}

// serve answers a request the agent made of us.
func (c *rpcConn) serve(m message) {
	var result any
	rpcErr := &RPCError{Code: -32601, Message: "method not found: " + m.Method}
	if c.onRequest != nil {
		result, rpcErr = c.onRequest(m.Method, m.Params)
	}
	reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
	if rpcErr != nil {
		reply["error"] = rpcErr
	} else {
		if result == nil {
			result = json.RawMessage("null")
		}
		reply["result"] = result
	}
	_ = c.write(reply)
}

func (c *rpcConn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.out.Write(b)
	return err
}

// call sends a request and waits for its response, the agent's exit, or ctx.
func (c *rpcConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.closed {
		err := c.exitErr
		c.mu.Unlock()
		return nil, err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		forget()
		return nil, fmt.Errorf("write to agent: %w", err)
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	case <-c.done:
		forget()
		// The response may have arrived just before the stream ended.
		select {
		case r := <-ch:
			return r.result, r.err
		default:
		}
		return nil, c.exitError()
	}
}

func (c *rpcConn) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *rpcConn) exitError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// shutdown fails every waiting call. It is idempotent.
func (c *rpcConn) shutdown(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if cause == nil {
		cause = ErrAgentExited
	}
	c.exitErr = cause
	pending := c.pending
	c.pending = map[int64]chan response{}
	c.mu.Unlock()
	close(c.done)
	for _, ch := range pending {
		ch <- response{err: cause}
	}
}

// ErrAgentExited means the agent's output ended: it exited or crashed.
var ErrAgentExited = errors.New("the agent process ended")
