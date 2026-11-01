package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

type fakeThreadHost struct {
	bound map[string]string // key → session
}

func (f *fakeThreadHost) SubscribeThread(_ context.Context, sid, key, _ string) (any, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("thread key is required")
	}
	f.bound[key] = sid
	return map[string]string{"key": key, "session_id": sid}, nil
}
func (f *fakeThreadHost) UnsubscribeThread(_ context.Context, sid, key string) error {
	if f.bound[key] != sid {
		return errors.New("this thread is bound to another session")
	}
	delete(f.bound, key)
	return nil
}
func (f *fakeThreadHost) ListThreads(_ context.Context, sid string) (any, error) {
	var out []string
	for k, s := range f.bound {
		if s == sid {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestThreadSubscribeBindsTheCallingSession(t *testing.T) {
	h := &fakeThreadHost{bound: map[string]string{}}
	SetThreadHandler(h)
	defer SetThreadHandler(nil)
	r := NewRegistry()
	if err := RegisterThreadSubscribeTool(r); err != nil {
		t.Fatal(err)
	}
	ctx := governance.WithSessionID(context.Background(), "sess_a")
	out := invokeByName(t, r, ctx, "thread_subscribe", `{"action":"subscribe","key":"github:acme/app#12","title":"PR"}`)
	if !strings.Contains(out, `"success":true`) || h.bound["github:acme/app#12"] != "sess_a" {
		t.Fatalf("subscribe: %s bound=%v", out, h.bound)
	}
	other := governance.WithSessionID(context.Background(), "sess_b")
	if out := invokeByName(t, r, other, "thread_subscribe", `{"action":"unsubscribe","key":"github:acme/app#12"}`); strings.Contains(out, `"success":true`) {
		t.Fatalf("another session unsubscribed it: %s", out)
	}
	if out := invokeByName(t, r, ctx, "thread_subscribe", `{"action":"list"}`); !strings.Contains(out, "github:acme/app#12") {
		t.Fatalf("list: %s", out)
	}
	if out := invokeByName(t, r, ctx, "thread_subscribe", `{"action":"unsubscribe","key":"github:acme/app#12"}`); !strings.Contains(out, `"success":true`) {
		t.Fatalf("owner unsubscribe: %s", out)
	}
}

func TestThreadSubscribeWithoutAHostAndUnknownAction(t *testing.T) {
	SetThreadHandler(nil)
	r := NewRegistry()
	_ = RegisterThreadSubscribeTool(r)
	if out := invokeByName(t, r, context.Background(), "thread_subscribe", `{"action":"list"}`); !strings.Contains(out, "not available") {
		t.Fatalf("no host: %s", out)
	}
	SetThreadHandler(&fakeThreadHost{bound: map[string]string{}})
	defer SetThreadHandler(nil)
	if out := invokeByName(t, r, context.Background(), "thread_subscribe", `{"action":"dance"}`); !strings.Contains(out, "unknown action") {
		t.Fatalf("unknown action: %s", out)
	}
	if out := invokeByName(t, r, context.Background(), "thread_subscribe", `{"action":"subscribe","key":" "}`); strings.Contains(out, `"success":true`) {
		t.Fatalf("an empty key was accepted: %s", out)
	}
}
