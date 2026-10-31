package browsercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeBrowser is a WebSocket endpoint that answers CDP commands by a script.
type fakeBrowser struct {
	srv    *httptest.Server
	mu     sync.Mutex
	conn   *websocket.Conn
	handle func(m cdpMsg, reply func(result any, errMsg string), emit func(method, session string, params any))
	ready  chan struct{}
}

func newFakeBrowser(t *testing.T, handle func(m cdpMsg, reply func(result any, errMsg string), emit func(method, session string, params any))) *fakeBrowser {
	t.Helper()
	f := &fakeBrowser{handle: handle, ready: make(chan struct{})}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = c
		f.mu.Unlock()
		close(f.ready)
		var wmu sync.Mutex
		send := func(v any) {
			wmu.Lock()
			defer wmu.Unlock()
			_ = c.WriteJSON(v)
		}
		for {
			var m cdpMsg
			if err := c.ReadJSON(&m); err != nil {
				return
			}
			go f.handle(m,
				func(result any, errMsg string) {
					if errMsg != "" {
						send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": errMsg}})
						return
					}
					send(map[string]any{"id": m.ID, "result": result})
				},
				func(method, session string, params any) {
					send(map[string]any{"method": method, "sessionId": session, "params": params})
				})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBrowser) url() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") }

func (f *fakeBrowser) drop() {
	<-f.ready
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.conn.Close()
}

func dial(t *testing.T, f *fakeBrowser) *cdpConn {
	t.Helper()
	c, err := dialCDP(context.Background(), f.url())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

func TestCallReturnsTheResultOfItsOwnCommand(t *testing.T) {
	f := newFakeBrowser(t, func(m cdpMsg, reply func(any, string), _ func(string, string, any)) {
		reply(map[string]any{"echo": m.Method, "session": m.SessionID}, "")
	})
	c := dial(t, f)
	var out struct{ Echo, Session string }
	if err := c.call(context.Background(), "S1", "Page.enable", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Echo != "Page.enable" || out.Session != "S1" {
		t.Fatalf("%+v", out)
	}
}

func TestConcurrentCallsAreMatchedToTheirOwnAnswers(t *testing.T) {
	f := newFakeBrowser(t, func(m cdpMsg, reply func(any, string), _ func(string, string, any)) {
		var p struct{ N int }
		_ = json.Unmarshal(m.Params, &p)
		// Answer in reverse order of arrival: larger N sooner.
		time.Sleep(time.Duration(10-p.N) * 8 * time.Millisecond)
		reply(map[string]any{"n": p.N}, "")
	})
	c := dial(t, f)
	var wg sync.WaitGroup
	errs := make(chan string, 10)
	for n := 1; n <= 9; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var out struct{ N int }
			if err := c.call(context.Background(), "", "X.y", map[string]any{"n": n}, &out); err != nil || out.N != n {
				errs <- "wrong answer"
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestAnErrorAnswerBecomesAnError(t *testing.T) {
	f := newFakeBrowser(t, func(_ cdpMsg, reply func(any, string), _ func(string, string, any)) {
		reply(nil, "No node with given id")
	})
	c := dial(t, f)
	err := c.call(context.Background(), "", "DOM.x", nil, nil)
	var ce *cdpError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "No node") {
		t.Fatalf("%v", err)
	}
}

func TestEventsReachSubscribersUntilTheyUnsubscribe(t *testing.T) {
	f := newFakeBrowser(t, func(m cdpMsg, reply func(any, string), emit func(string, string, any)) {
		emit("Page.loadEventFired", m.SessionID, map[string]any{"timestamp": 1})
		reply(map[string]any{}, "")
	})
	c := dial(t, f)
	got := make(chan cdpEvent, 4)
	off := c.on(func(e cdpEvent) { got <- e })
	if err := c.call(context.Background(), "S9", "Page.reload", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		if e.Method != "Page.loadEventFired" || e.Session != "S9" {
			t.Fatalf("%+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	off()
	_ = c.call(context.Background(), "S9", "Page.reload", nil, nil)
	select {
	case e := <-got:
		t.Fatalf("event after unsubscribe: %+v", e)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestACallEndsWhenItsContextEnds(t *testing.T) {
	f := newFakeBrowser(t, func(cdpMsg, func(any, string), func(string, string, any)) { /* never answers */ })
	c := dial(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.call(ctx, "", "Slow.call", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d abandoned calls were left registered", left)
	}
}

func TestLosingTheBrowserFailsWaitingAndLaterCalls(t *testing.T) {
	f := newFakeBrowser(t, func(cdpMsg, func(any, string), func(string, string, any)) { /* never answers */ })
	c := dial(t, f)
	result := make(chan error, 1)
	go func() { result <- c.call(context.Background(), "", "Hang.forever", nil, nil) }()
	time.Sleep(60 * time.Millisecond)
	f.drop()
	select {
	case err := <-result:
		if !errors.Is(err, errConnClosed) {
			t.Fatalf("%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a call kept waiting after the browser went away")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
	if err := c.call(context.Background(), "", "After.close", nil, nil); !errors.Is(err, errConnClosed) {
		t.Fatalf("%v", err)
	}
}
