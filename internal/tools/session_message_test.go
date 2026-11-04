package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

type fakeSessions struct {
	from, to, text string
	listLimit      int
	readLimit      int
}

func (f *fakeSessions) ListSessions(_ context.Context, self, q string, limit int) (any, error) {
	f.listLimit = limit
	return []string{self, q}, nil
}
func (f *fakeSessions) SendSessionMessage(_ context.Context, from, to, text string) (any, error) {
	if to == "gone" {
		return nil, errors.New(`no conversation "gone"`)
	}
	f.from, f.to, f.text = from, to, text
	return map[string]string{"delivered_to": to}, nil
}
func (f *fakeSessions) ReadSession(_ context.Context, _, _ string, limit int) (any, error) {
	f.readLimit = limit
	return "ok", nil
}

func newSessionTool(t *testing.T) (*Registry, *fakeSessions) {
	t.Helper()
	r := NewRegistry()
	if err := RegisterSessionMessageTool(r); err != nil {
		t.Fatal(err)
	}
	f := &fakeSessions{}
	SetSessionMessageHandler(f)
	t.Cleanup(func() { SetSessionMessageHandler(nil) })
	return r, f
}

func TestSessionMessageSendUsesTheCallersSession(t *testing.T) {
	r, f := newSessionTool(t)
	ctx := governance.WithSessionID(context.Background(), "sess-A")
	out := invokeByName(t, r, ctx, "session_message", `{"action":"send","to":"sess-B","text":"  请同步字段  "}`)
	if !strings.Contains(out, `"success":true`) || f.from != "sess-A" || f.to != "sess-B" || f.text != "请同步字段" {
		t.Fatalf("out=%s from=%q to=%q text=%q", out, f.from, f.to, f.text)
	}
}

func TestSessionMessageLimitsAreClamped(t *testing.T) {
	r, f := newSessionTool(t)
	ctx := governance.WithSessionID(context.Background(), "s")
	invokeByName(t, r, ctx, "session_message", `{"action":"list"}`)
	if f.listLimit != 15 {
		t.Fatalf("default list limit %d", f.listLimit)
	}
	invokeByName(t, r, ctx, "session_message", `{"action":"list","limit":9999}`)
	if f.listLimit != 50 {
		t.Fatalf("list limit %d", f.listLimit)
	}
	invokeByName(t, r, ctx, "session_message", `{"action":"read","to":"x","limit":500}`)
	if f.readLimit != 30 {
		t.Fatalf("read limit %d", f.readLimit)
	}
}

func TestSessionMessageRejectsBadInput(t *testing.T) {
	r, _ := newSessionTool(t)
	ctx := governance.WithSessionID(context.Background(), "s")
	for name, args := range map[string]string{
		"send without text": `{"action":"send","to":"b"}`,
		"send without to":   `{"action":"send","text":"hi"}`,
		"read without to":   `{"action":"read"}`,
		"too long":          `{"action":"send","to":"b","text":"` + strings.Repeat("字", MaxSessionMessageChars+1) + `"}`,
		"unknown target":    `{"action":"send","to":"gone","text":"hi"}`,
		"unknown action":    `{"action":"shout"}`,
	} {
		if out := invokeByName(t, r, ctx, "session_message", args); strings.Contains(out, `"success":true`) || !strings.Contains(out, `"error"`) {
			t.Errorf("%s should fail: %s", name, out)
		}
	}
	if out := invokeByName(t, r, ctx, "session_message", `{"action":"send","to":"gone","text":"hi"}`); !strings.Contains(out, `no conversation`) {
		t.Errorf("the host's reason must reach the model: %s", out)
	}
}

func TestSessionMessageWithoutHost(t *testing.T) {
	r := NewRegistry()
	_ = RegisterSessionMessageTool(r)
	SetSessionMessageHandler(nil)
	if out := invokeByName(t, r, context.Background(), "session_message", `{"action":"list"}`); !strings.Contains(out, "not available") {
		t.Fatalf("got %s", out)
	}
}
