package browsercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// A minimal Chrome DevTools Protocol client: one WebSocket to the browser,
// request/response matching by id, and events handed to subscribers. Nothing
// here is specific to a page or to AgentGo.

var errConnClosed = errors.New("browser connection closed")

// cdpError is an error answer from the browser.
type cdpError struct {
	Code    int
	Message string
}

func (e *cdpError) Error() string { return fmt.Sprintf("browser: %s (code %d)", e.Message, e.Code) }

type cdpEvent struct {
	Method  string
	Session string
	Params  json.RawMessage
}

type cdpMsg struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type cdpConn struct {
	ws      *websocket.Conn
	wmu     sync.Mutex // serialises writes
	seq     atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMsg
	subs    map[int]func(cdpEvent)
	subSeq  int
	done    chan struct{}
	once    sync.Once
}

const (
	cdpWriteTimeout = 10 * time.Second
	cdpReadLimit    = 64 << 20 // a full-page screenshot arrives as one message
)

func dialCDP(ctx context.Context, wsURL string) (*cdpConn, error) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := d.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	ws.SetReadLimit(cdpReadLimit)
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpMsg{}, subs: map[int]func(cdpEvent){}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdpConn) readLoop() {
	defer c.shutdown()
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var m cdpMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m // buffered, one per call
			}
			continue
		}
		if m.Method == "" {
			continue
		}
		c.mu.Lock()
		fns := make([]func(cdpEvent), 0, len(c.subs))
		for _, fn := range c.subs {
			fns = append(fns, fn)
		}
		c.mu.Unlock()
		ev := cdpEvent{Method: m.Method, Session: m.SessionID, Params: m.Params}
		for _, fn := range fns {
			fn(ev)
		}
	}
}

func (c *cdpConn) shutdown() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close()
	})
}

// Done is closed when the connection ends, whoever ended it.
func (c *cdpConn) Done() <-chan struct{} { return c.done }

func (c *cdpConn) close() { c.shutdown() }

// on registers fn for every event. fn runs on the read loop: it must return
// quickly and must not call the browser, or the reply it waits for could never
// be read. Hand the work to another goroutine.
func (c *cdpConn) on(fn func(cdpEvent)) (off func()) {
	c.mu.Lock()
	c.subSeq++
	id := c.subSeq
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

// call sends a command and decodes its result into out (nil to ignore it).
// session is empty for browser-level commands.
func (c *cdpConn) call(ctx context.Context, session, method string, params, out any) error {
	select {
	case <-c.done:
		return errConnClosed
	default:
	}
	id := c.seq.Add(1)
	body := map[string]any{"id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	if session != "" {
		body["sessionId"] = session
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ch := make(chan cdpMsg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	c.wmu.Lock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(cdpWriteTimeout))
	err = c.ws.WriteMessage(websocket.TextMessage, raw)
	c.wmu.Unlock()
	if err != nil {
		forget()
		c.shutdown()
		return errConnClosed
	}

	select {
	case m := <-ch:
		if m.Error != nil {
			return &cdpError{Code: m.Error.Code, Message: m.Error.Message}
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		forget()
		return ctx.Err()
	case <-c.done:
		forget()
		return errConnClosed
	}
}
