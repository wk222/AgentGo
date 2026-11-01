package bridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/gateway"
	"agentgo/internal/threadsub"
)

type wakeCapture struct {
	mu   sync.Mutex
	got  []string
	seen chan struct{}
}

func (w *wakeCapture) handle(session, text string) {
	w.mu.Lock()
	w.got = append(w.got, session+"\x00"+text)
	w.mu.Unlock()
	w.seen <- struct{}{}
}

func threadRuntime(t *testing.T) (*Runtime, *wakeCapture) {
	t.Helper()
	rt, err := newRuntimeAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.threads == nil || rt.AgentRunner() == nil {
		t.Fatal("the runtime should have thread bindings and a runner")
	}
	c := &wakeCapture{seen: make(chan struct{}, 8)}
	rt.AgentRunner().SetBackgroundWakeHandler(c.handle)
	return rt, c
}

func (w *wakeCapture) wait(t *testing.T) (session, text string) {
	t.Helper()
	select {
	case <-w.seen:
	case <-time.After(10 * time.Second):
		t.Fatal("the event never reached a session")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	last := w.got[len(w.got)-1]
	session, text, _ = strings.Cut(last, "\x00")
	return session, text
}

func TestThreadEventGoesToTheSessionThatFollowsIt(t *testing.T) {
	rt, c := threadRuntime(t)
	s, err := rt.Sessions().Create(context.Background(), "PR 12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SubscribeThread(context.Background(), s.ID, "github:acme/app#12", "PR 12"); err != nil {
		t.Fatal(err)
	}
	sid, err := rt.DeliverThreadEvent(context.Background(), "github:acme/app#12", "github", "alice: please rename x", false)
	if err != nil || sid != s.ID {
		t.Fatalf("sid=%q err=%v, want %q", sid, err, s.ID)
	}
	session, text := c.wait(t)
	if session != s.ID || !strings.Contains(text, "alice: please rename x") || !strings.Contains(text, "github:acme/app#12") {
		t.Fatalf("delivered to %q: %q", session, text)
	}
	if sub, _ := rt.threads.Resolve("github:acme/app#12"); sub.Events != 1 {
		t.Fatalf("delivery not counted: %+v", sub)
	}
}

func TestUnknownThreadIsRefusedUnlessItMayCreateASession(t *testing.T) {
	rt, c := threadRuntime(t)
	_, err := rt.DeliverThreadEvent(context.Background(), "mail:<x@y>", "mail", "hello", false)
	if !errors.Is(err, gateway.ErrThreadNotFound) {
		t.Fatalf("want ErrThreadNotFound, got %v", err)
	}
	sid, err := rt.DeliverThreadEvent(context.Background(), "mail:<x@y>", "mail", "hello", true)
	if err != nil || sid != threadsub.SessionFor("mail:<x@y>") {
		t.Fatalf("sid=%q err=%v", sid, err)
	}
	session, _ := c.wait(t)
	if session != sid {
		t.Fatalf("woke %q, want %q", session, sid)
	}
	// The thread is now bound: the next event needs no create flag and lands in the same session.
	sid2, err := rt.DeliverThreadEvent(context.Background(), "mail:<x@y>", "mail", "second", false)
	if err != nil || sid2 != sid {
		t.Fatalf("second event: %q %v", sid2, err)
	}
	if _, err := rt.Sessions().Get(context.Background(), sid); err != nil {
		t.Fatalf("the thread's session was not created: %v", err)
	}
}

func TestThreadEventNeedsTextAndAKey(t *testing.T) {
	rt, _ := threadRuntime(t)
	if _, err := rt.DeliverThreadEvent(context.Background(), "k", "s", "  ", true); err == nil {
		t.Fatal("an empty event was accepted")
	}
	if _, err := rt.DeliverThreadEvent(context.Background(), " ", "s", "x", true); err == nil {
		t.Fatal("an empty key was accepted")
	}
}

func TestOnlyTheFollowingSessionMayUnsubscribeFromTheTool(t *testing.T) {
	rt, _ := threadRuntime(t)
	if _, err := rt.SubscribeThread(context.Background(), "s1", "k", ""); err != nil {
		t.Fatal(err)
	}
	if err := rt.UnsubscribeThread(context.Background(), "s2", "k"); err == nil {
		t.Fatal("another session unsubscribed the thread")
	}
	if err := rt.UnsubscribeThread(context.Background(), "s1", "k"); err != nil {
		t.Fatal(err)
	}
}
