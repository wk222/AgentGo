package bridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSessionMessageReachesTheTargetTaggedWithItsSender(t *testing.T) {
	rt, c := threadRuntime(t)
	ctx := context.Background()
	a, _ := rt.Sessions().Create(ctx, "前端重构")
	b, _ := rt.Sessions().Create(ctx, "后端接口")

	res, err := rt.SendSessionMessage(ctx, a.ID, b.ID, "接口字段 user_id 改成 uid 了，请同步")
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["delivered_to"] != b.ID {
		t.Fatalf("result: %v", res)
	}
	session, text := c.wait(t)
	if session != b.ID {
		t.Fatalf("delivered to %q, want %q", session, b.ID)
	}
	for _, want := range []string{"前端重构", a.ID, "user_id 改成 uid", "session_message", "to=" + a.ID} {
		if !strings.Contains(text, want) {
			t.Errorf("the delivered turn lacks %q: %q", want, text)
		}
	}
}

func TestSessionMessageRefusals(t *testing.T) {
	rt, _ := threadRuntime(t)
	ctx := context.Background()
	a, _ := rt.Sessions().Create(ctx, "A")
	for name, call := range map[string]func() error{
		"to itself":    func() error { _, err := rt.SendSessionMessage(ctx, a.ID, a.ID, "hi"); return err },
		"unknown":      func() error { _, err := rt.SendSessionMessage(ctx, a.ID, "sess_nope", "hi"); return err },
		"no sender":    func() error { _, err := rt.SendSessionMessage(ctx, "", a.ID, "hi"); return err },
		"read unknown": func() error { _, err := rt.ReadSession(ctx, a.ID, "sess_nope", 5); return err },
	} {
		if call() == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestSessionMessageIsRateLimitedPerPair(t *testing.T) {
	rt, c := threadRuntime(t)
	ctx := context.Background()
	a, _ := rt.Sessions().Create(ctx, "A")
	b, _ := rt.Sessions().Create(ctx, "B")
	other, _ := rt.Sessions().Create(ctx, "C")
	for i := 0; i < sessionMsgPairLimit; i++ {
		if _, err := rt.SendSessionMessage(ctx, a.ID, b.ID, "ping"); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	if _, err := rt.SendSessionMessage(ctx, a.ID, b.ID, "ping"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("a ping-pong loop must be cut off: %v", err)
	}
	// Another target is still reachable, and the reverse direction has its own budget.
	if _, err := rt.SendSessionMessage(ctx, a.ID, other.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SendSessionMessage(ctx, b.ID, a.ID, "pong"); err != nil {
		t.Fatal(err)
	}
	c.wait(t)
}

func TestListAndReadSessions(t *testing.T) {
	rt, _ := threadRuntime(t)
	ctx := context.Background()
	a, _ := rt.Sessions().Create(ctx, "当前会话")
	b, _ := rt.Sessions().Create(ctx, "数据库迁移")
	_, _ = rt.Sessions().Create(ctx, "写周报")
	_ = rt.Sessions().AppendMessage(ctx, b.ID, "user", "把 users 表拆成两张", "text", nil)
	_ = rt.Sessions().AppendMessage(ctx, b.ID, "assistant", strings.Repeat("好的。", 800), "text", nil)

	all, err := rt.ListSessions(ctx, a.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	rows := all.([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("a conversation must not list itself: %v", rows)
	}
	for _, r := range rows {
		if r["id"] == a.ID {
			t.Fatal("listed itself")
		}
	}
	filtered, _ := rt.ListSessions(ctx, a.ID, "迁移", 10)
	if got := filtered.([]map[string]any); len(got) != 1 || got[0]["id"] != b.ID {
		t.Fatalf("title filter: %v", got)
	}
	if one, _ := rt.ListSessions(ctx, a.ID, "", 1); len(one.([]map[string]any)) != 1 {
		t.Fatal("limit ignored")
	}
	// A topic word that is in no title must not come back as an empty list: the model would stop
	// there. It gets the recent conversations and a note saying how to look inside them.
	fallback, _ := rt.ListSessions(ctx, a.ID, "launch code", 10)
	fb, ok := fallback.(map[string]any)
	if !ok || fb["note"] == "" || len(fb["sessions"].([]map[string]any)) != 2 {
		t.Fatalf("no title match must fall back to the recent list with a note: %#v", fallback)
	}

	read, err := rt.ReadSession(ctx, a.ID, b.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	msgs := read.(map[string]any)["messages"].([]map[string]any)
	if len(msgs) != 2 || msgs[0]["role"] != "user" {
		t.Fatalf("messages: %v", msgs)
	}
	if long := msgs[1]["text"].(string); !strings.HasSuffix(long, "（已截断）") || len([]rune(long)) > sessionMsgReadRunes+20 {
		t.Fatalf("a long message must be cut: %d runes", len([]rune(long)))
	}
}

func TestRateWindowForgetsOldHits(t *testing.T) {
	var w rateWindow
	now := time.Now()
	rule := rateRule{key: "k", limit: 2}
	if !w.allow(now, time.Minute, rule) || !w.allow(now, time.Minute, rule) {
		t.Fatal("two hits fit")
	}
	if w.allow(now, time.Minute, rule) {
		t.Fatal("the third must not")
	}
	if !w.allow(now.Add(2*time.Minute), time.Minute, rule) {
		t.Fatal("old hits must expire")
	}
	// A full rule blocks the whole call and records nothing under the others.
	full := rateRule{key: "full", limit: 1}
	_ = w.allow(now, time.Minute, full)
	if w.allow(now, time.Minute, rateRule{key: "free", limit: 5}, full) {
		t.Fatal("a full rule must refuse the call")
	}
	if got := len(w.hits["free"]); got != 0 {
		t.Fatalf("a refused call counted against another rule: %d", got)
	}
}
