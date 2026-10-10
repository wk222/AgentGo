package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/governance"
)

type fakeWatch struct {
	created []WatchRequest
	owner   map[string]string
}

func (f *fakeWatch) CreateWatch(_ context.Context, r WatchRequest) (any, error) {
	f.created = append(f.created, r)
	id := "trg_x"
	f.owner[id] = r.SessionID
	return map[string]string{"id": id}, nil
}

func (f *fakeWatch) ListWatches(_ context.Context, sid string) (any, error) {
	return []string{sid}, nil
}

func (f *fakeWatch) CancelWatch(_ context.Context, sid, id string) error {
	if f.owner[id] != sid {
		return context.Canceled
	}
	return nil
}

func invokeByName(t *testing.T, r *Registry, ctx context.Context, name, args string) string {
	t.Helper()
	bt, ok := r.Get(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	it, ok := bt.(einotool.InvokableTool)
	if !ok {
		t.Fatalf("tool %s is not invokable", name)
	}
	out, err := it.InvokableRun(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWatchEventToolSchema(t *testing.T) {
	r := NewRegistry()
	if err := RegisterWatchEventTool(r); err != nil {
		t.Fatal(err)
	}
	bt, ok := r.Get("watch_event")
	if !ok {
		t.Fatal("watch_event not registered")
	}
	info, err := bt.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(sc)
	if !strings.Contains(string(js), "log_match") {
		t.Fatalf("schema lost the kind description: %s", js)
	}
}

func TestWatchEventCreateBindsToCallerSession(t *testing.T) {
	f := &fakeWatch{owner: map[string]string{}}
	SetWatchHandler(f)
	defer SetWatchHandler(nil)

	r := NewRegistry()
	if err := RegisterWatchEventTool(r); err != nil {
		t.Fatal(err)
	}
	ctx := governance.WithSessionID(context.Background(), "sess_abc")
	out := invokeByName(t, r, ctx, "watch_event",
		`{"action":"create","kind":"file","path":"C:\\x\\done.flag","prompt":"analyse","timeout_hours":9999}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("create failed: %s", out)
	}
	if len(f.created) != 1 || f.created[0].SessionID != "sess_abc" {
		t.Fatalf("trigger must bind to the calling session: %+v", f.created)
	}
	if f.created[0].ExpiresInSec != int64(maxWatchHours*3600) {
		t.Fatalf("timeout must be clamped to %vh, got %ds", maxWatchHours, f.created[0].ExpiresInSec)
	}
	other := governance.WithSessionID(context.Background(), "sess_other")
	if out := invokeByName(t, r, other, "watch_event", `{"action":"cancel","id":"trg_x"}`); strings.Contains(out, `"success":true`) {
		t.Fatalf("cross-session cancel must fail: %s", out)
	}
	if out := invokeByName(t, r, ctx, "watch_event", `{"action":"cancel","id":"trg_x"}`); !strings.Contains(out, `"success":true`) {
		t.Fatalf("own cancel should work: %s", out)
	}
}

func TestWatchEventWithoutHandler(t *testing.T) {
	SetWatchHandler(nil)
	r := NewRegistry()
	_ = RegisterWatchEventTool(r)
	out := invokeByName(t, r, context.Background(), "watch_event", `{"action":"list"}`)
	if !strings.Contains(out, "not available") {
		t.Fatalf("expected a clear error, got %s", out)
	}
}
