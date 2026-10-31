package im

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/gateway"
	channel "agentgo/internal/imchannel"
)

// fakeAdapter 记录发出去的东西，并能手动注入入站消息。
type fakeAdapter struct {
	mu      sync.Mutex
	texts   []string
	files   []string
	buttons [][]channel.Button
	onMsg   func(channel.InboundEvent)
	ready   chan struct{}
	stop    chan struct{}
	btn     bool
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{ready: make(chan struct{}), stop: make(chan struct{})}
}

func (f *fakeAdapter) Platform() string { return "fake" }
func (f *fakeAdapter) Start(ctx context.Context, on func(channel.InboundEvent)) error {
	f.mu.Lock()
	f.onMsg = on
	f.mu.Unlock()
	close(f.ready)
	select {
	case <-ctx.Done():
	case <-f.stop:
	}
	return nil
}
func (f *fakeAdapter) Stop() error {
	select {
	case <-f.stop:
	default:
		close(f.stop)
	}
	return nil
}
func (f *fakeAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	f.mu.Lock()
	f.texts = append(f.texts, chatID+": "+text)
	f.mu.Unlock()
	return channel.SendResult{OK: true}
}
func (f *fakeAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	f.mu.Lock()
	f.files = append(f.files, chatID+": "+path)
	f.mu.Unlock()
	return channel.SendResult{OK: true}
}
func (f *fakeAdapter) UpdateMessage(chatID, messageID, text string) bool { return false }
func (f *fakeAdapter) SupportsButtons() bool                             { return f.btn }
func (f *fakeAdapter) SendButtons(chatID, text string, b []channel.Button, replyTo string) channel.SendResult {
	f.mu.Lock()
	f.buttons = append(f.buttons, b)
	f.texts = append(f.texts, chatID+": "+text)
	f.mu.Unlock()
	return channel.SendResult{OK: true}
}
func (f *fakeAdapter) SupportsMessageUpdates() bool                       { return false }
func (f *fakeAdapter) SendTyping(chatID, ct string) error                 { return nil }
func (f *fakeAdapter) StopTyping(chatID, ct string) error                 { return nil }
func (f *fakeAdapter) Flush(chatID string)                                {}
func (f *fakeAdapter) ValidateConfig(cfg channel.PlatformConfig) []string { return nil }

func (f *fakeAdapter) say(ev channel.InboundEvent) {
	<-f.ready
	f.mu.Lock()
	on := f.onMsg
	f.mu.Unlock()
	on(ev)
}

func (f *fakeAdapter) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func (f *fakeAdapter) waitText(t *testing.T, contains string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range f.sent() {
			if strings.Contains(s, contains) {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("没有收到包含 %q 的消息，已发出: %q", contains, f.sent())
	return ""
}

// fakeBackend 同时实现 Backend 与 ApprovalResolver。
type fakeBackend struct {
	mu        sync.Mutex
	sessions  []string
	messages  []string
	reply     func(req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error)
	cancelled []string
	resolved  []string
}

func (b *fakeBackend) StreamChat(ctx context.Context, req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error) {
	b.mu.Lock()
	b.sessions = append(b.sessions, req.SessionID)
	b.messages = append(b.messages, req.Message)
	b.mu.Unlock()
	if b.reply != nil {
		return b.reply(req, emit)
	}
	return &gateway.ChatResult{Content: "echo: " + req.Message}, nil
}
func (b *fakeBackend) CancelSessionRun(ctx context.Context, sessionID string) bool {
	b.mu.Lock()
	b.cancelled = append(b.cancelled, sessionID)
	b.mu.Unlock()
	return true
}
func (b *fakeBackend) ResolveApproval(ctx context.Context, id string, approve bool, emit func(string, []byte) error) (*gateway.ChatResult, error) {
	b.mu.Lock()
	b.resolved = append(b.resolved, id+":"+map[bool]string{true: "approve", false: "deny"}[approve])
	b.mu.Unlock()
	return &gateway.ChatResult{Content: "resumed"}, nil
}

func startManager(t *testing.T, be Backend, cfg map[string]any, ad *fakeAdapter) *Manager {
	t.Helper()
	m := NewManager(be)
	m.factory = func(platform string, c channel.PlatformConfig) (channel.Adapter, error) { return ad, nil }
	m.load = func() (*channel.Config, error) {
		return &channel.Config{Channels: map[string]channel.InstanceList{
			"fake": {{Name: "bot1", Enabled: true, Config: cfg}},
		}}, nil
	}
	n, err := m.Start(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	t.Cleanup(m.Stop)
	return m
}

func TestAccessParsing(t *testing.T) {
	a := ParseAccess(map[string]any{"allow_from": []any{"u1", 42, " u3 "}})
	if !a.Permits("u1", "") || !a.Permits("42", "") || !a.Permits("u3", "") {
		t.Fatalf("list parsing failed: %+v", a.AllowFrom)
	}
	if a.Permits("evil", "chat") {
		t.Fatal("stranger must be denied")
	}
	if a.Permits("", "") {
		t.Fatal("empty ids must not match")
	}
	s := ParseAccess(map[string]any{"allow_from": "a, b;c"})
	if !s.Permits("b", "") || !s.Permits("c", "") {
		t.Fatalf("string parsing failed: %+v", s.AllowFrom)
	}
	if !ParseAccess(map[string]any{"allow_all": true}).Permits("x", "y") {
		t.Fatal("allow_all must allow")
	}
	if ParseAccess(nil).Permits("x", "y") {
		t.Fatal("default must deny")
	}
}

func TestSessionIDRoundTrip(t *testing.T) {
	for _, gen := range []int{0, 3} {
		id := SessionID("bot1", "chat:42", gen)
		a, c, ok := ParseSessionID(id)
		if !ok || a != "bot1" || c != "chat:42" {
			t.Fatalf("gen %d: %q -> %q %q %v", gen, id, a, c, ok)
		}
	}
	if _, _, ok := ParseSessionID("session-123"); ok {
		t.Fatal("non-im session must not parse")
	}
}

func TestDefaultDeniesAndSaysWhoYouAre(t *testing.T) {
	ad, be := newFakeAdapter(), &fakeBackend{}
	startManager(t, be, map[string]any{}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "stranger", Text: "rm -rf /"})
	got := ad.waitText(t, "未授权")
	if !strings.Contains(got, "stranger") {
		t.Fatalf("denial should tell the user their id: %s", got)
	}
	// 再发一次不应再回复（限频），也绝不能进入 agent
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "stranger", Text: "again"})
	time.Sleep(100 * time.Millisecond)
	be.mu.Lock()
	n := len(be.messages)
	be.mu.Unlock()
	if n != 0 {
		t.Fatalf("unauthorised message reached the agent: %v", be.messages)
	}
	if len(ad.sent()) != 1 {
		t.Fatalf("denial reply must be rate limited, got %q", ad.sent())
	}
}

func TestAllowedMessageRunsInIMSession(t *testing.T) {
	ad, be := newFakeAdapter(), &fakeBackend{}
	startManager(t, be, map[string]any{"allow_from": []any{"u1"}}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "你好"})
	ad.waitText(t, "echo: 你好")
	be.mu.Lock()
	defer be.mu.Unlock()
	if be.sessions[0] != "im:bot1:c1" {
		t.Fatalf("session = %q", be.sessions[0])
	}
}

func TestNewCommandSwitchesSession(t *testing.T) {
	ad, be := newFakeAdapter(), &fakeBackend{}
	startManager(t, be, map[string]any{"allow_from": []any{"u1"}}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "/new"})
	ad.waitText(t, "新会话")
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "hi"})
	ad.waitText(t, "echo: hi")
	be.mu.Lock()
	defer be.mu.Unlock()
	if be.sessions[0] != "im:bot1:c1#1" {
		t.Fatalf("after /new session = %q", be.sessions[0])
	}
}

func TestApprovalFlowWithTextCommands(t *testing.T) {
	ad := newFakeAdapter()
	be := &fakeBackend{}
	be.reply = func(req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error) {
		payload, _ := json.Marshal(map[string]string{"approval_id": "ap-9", "tool_name": "execute_bash", "arguments": `{"command":"ls"}`})
		_ = emit("interrupt", payload)
		return &gateway.ChatResult{}, nil
	}
	startManager(t, be, map[string]any{"allow_from": []any{"u1"}}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "list files"})
	ad.waitText(t, "execute_bash")
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "/approve"})
	ad.waitText(t, "resumed")
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.resolved) != 1 || be.resolved[0] != "ap-9:approve" {
		t.Fatalf("resolved = %v", be.resolved)
	}
}

func TestApprovalButtons(t *testing.T) {
	ad := newFakeAdapter()
	ad.btn = true
	be := &fakeBackend{}
	be.reply = func(req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error) {
		payload, _ := json.Marshal(map[string]string{"approval_id": "ap-1", "tool_name": "write_file"})
		_ = emit("interrupt", payload)
		return &gateway.ChatResult{}, nil
	}
	startManager(t, be, map[string]any{"allow_from": []any{"u1"}}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "write"})
	ad.waitText(t, "write_file")
	ad.mu.Lock()
	if len(ad.buttons) != 1 || ad.buttons[0][0].ID != "approve:ap-1" || ad.buttons[0][1].ID != "deny:ap-1" {
		t.Fatalf("buttons = %+v", ad.buttons)
	}
	ad.mu.Unlock()
	// 按钮回调
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", ButtonID: "deny:ap-1"})
	ad.waitText(t, "resumed")
	be.mu.Lock()
	defer be.mu.Unlock()
	if be.resolved[0] != "ap-1:deny" {
		t.Fatalf("resolved = %v", be.resolved)
	}
}

func TestStopCancelsRunningTask(t *testing.T) {
	ad := newFakeAdapter()
	started := make(chan struct{})
	be := &fakeBackend{}
	var once sync.Once
	be.reply = func(req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error) {
		once.Do(func() { close(started) })
		time.Sleep(2 * time.Second) // 模拟长任务；/stop 通过 ctx 取消在 StreamChat 之外生效
		return &gateway.ChatResult{Content: "late"}, nil
	}
	// 让 StreamChat 感知 ctx：包一层
	wrapped := &ctxBackend{fakeBackend: be}
	startManager(t, wrapped, map[string]any{"allow_from": []any{"u1"}}, ad)
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "long"})
	<-started
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "again"})
	ad.waitText(t, "还在处理")
	ad.say(channel.InboundEvent{ChatID: "c1", UserID: "u1", Text: "/stop"})
	ad.waitText(t, "已中止")
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.cancelled) != 1 || be.cancelled[0] != "im:bot1:c1" {
		t.Fatalf("cancelled = %v", be.cancelled)
	}
}

// ctxBackend 让 StreamChat 在 ctx 取消时立刻返回，和真实后端一致。
type ctxBackend struct{ *fakeBackend }

func (c *ctxBackend) StreamChat(ctx context.Context, req gateway.ChatRequest, emit func(string, []byte) error) (*gateway.ChatResult, error) {
	type out struct {
		r *gateway.ChatResult
		e error
	}
	ch := make(chan out, 1)
	go func() {
		r, e := c.fakeBackend.StreamChat(ctx, req, emit)
		ch <- out{r, e}
	}()
	select {
	case o := <-ch:
		return o.r, o.e
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSendFileToSession(t *testing.T) {
	ad, be := newFakeAdapter(), &fakeBackend{}
	m := startManager(t, be, map[string]any{"allow_from": []any{"u1"}}, ad)
	f := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.SendFileToSession("im:bot1:c1", f, ""); err != nil {
		t.Fatal(err)
	}
	if len(ad.files) != 1 || !strings.HasPrefix(ad.files[0], "c1: ") {
		t.Fatalf("files = %v", ad.files)
	}
	if err := m.SendFileToSession("desktop-session", f, ""); !errors.Is(err, ErrNotIMSession) {
		t.Fatalf("non-im session: %v", err)
	}
	if err := m.SendFileToSession("im:ghost:c1", f, ""); err == nil {
		t.Fatal("unknown instance must fail")
	}
	if err := m.SendFileToSession("im:bot1:c1", filepath.Dir(f), ""); err == nil {
		t.Fatal("directory must be rejected")
	}
}
