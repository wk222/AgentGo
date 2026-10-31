// Package im 把 IM 平台（Telegram/飞书/钉钉/企微/微信/Discord）接成 agentgo 的双向入口：
// 平台消息 → 会话（im:<实例>:<会话ID>）→ agent → 回复发回平台。
//
// 适配器本身在 internal/imchannel（移植自 octo-agent，MIT）；这里是 agentgo 自己的
// 管理层：访问控制、命令、审批转发、/stop、文件回传。
package im

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"agentgo/internal/gateway"
	channel "agentgo/internal/imchannel"
)

// Backend 是管理层需要的最小后端能力（bridge.RuntimeGateway 满足它）。
type Backend interface {
	StreamChat(ctx context.Context, req gateway.ChatRequest, emit func(event string, data []byte) error) (*gateway.ChatResult, error)
	CancelSessionRun(ctx context.Context, sessionID string) bool
}

// 适配器通过 channel.Register 登记构造函数；测试里可以换成假的。
type adapterFactory func(platform string, cfg channel.PlatformConfig) (channel.Adapter, error)

func registryFactory(platform string, cfg channel.PlatformConfig) (channel.Adapter, error) {
	ctor, err := channel.Find(platform)
	if err != nil {
		return nil, err
	}
	return ctor(cfg)
}

// Manager 运行所有已启用的机器人实例。
type Manager struct {
	backend  Backend
	resolver gateway.ApprovalResolver // 可选
	factory  adapterFactory
	load     func() (*channel.Config, error)

	mu      sync.Mutex
	running map[string]*instance // adapterID → 实例
	chats   map[string]*chatState
	denied  map[string]time.Time // 未授权提示的限频
	cancel  context.CancelFunc
	parent  context.Context // 第一次 Start 传入的上下文，Reload 用它重新启动
	wg      sync.WaitGroup
}

type instance struct {
	id       string
	platform string
	adapter  channel.Adapter
	access   Access
}

type chatState struct {
	mu      sync.Mutex
	busy    bool
	cancel  context.CancelFunc
	gen     int      // /new 之后递增，换一个新会话
	pending []string // 等待批准的 approval id（最新在后）
}

// NewManager 创建管理层。backend 同时实现 gateway.ApprovalResolver 时启用审批转发。
func NewManager(backend Backend) *Manager {
	m := &Manager{
		backend: backend,
		factory: registryFactory,
		load:    channel.LoadConfig,
		running: map[string]*instance{},
		chats:   map[string]*chatState{},
		denied:  map[string]time.Time{},
	}
	if r, ok := backend.(gateway.ApprovalResolver); ok {
		m.resolver = r
	}
	return m
}

// Start 启动配置里所有已启用的实例，返回启动了几个。没有启用的实例不是错误。
func (m *Manager) Start(ctx context.Context) (int, error) {
	cfg, err := m.load()
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	if m.parent == nil {
		m.parent = ctx
	}
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	started := 0
	for _, ref := range cfg.EnabledInstances() {
		id := ref.AdapterID()
		ad, err := m.factory(ref.Platform, ref.Instance.Config)
		if err != nil {
			log.Printf("[im] %s: %v", id, err)
			continue
		}
		if errs := ad.ValidateConfig(ref.Instance.Config); len(errs) > 0 {
			log.Printf("[im] %s: invalid config: %s", id, strings.Join(errs, "; "))
			continue
		}
		access := ParseAccess(ref.Instance.Config)
		if !access.AllowAll && len(access.AllowFrom) == 0 {
			log.Printf("[im] %s: 未配置 allow_from，所有消息都会被拒绝（只回复其 ID 方便你加入白名单）", id)
		}
		inst := &instance{id: id, platform: ref.Platform, adapter: ad, access: access}
		m.mu.Lock()
		m.running[id] = inst
		m.mu.Unlock()
		m.wg.Add(1)
		go m.runInstance(ctx, inst)
		started++
	}
	return started, nil
}

// Stop 停掉所有实例并取消正在进行的运行。
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	insts := make([]*instance, 0, len(m.running))
	for _, i := range m.running {
		insts = append(insts, i)
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, i := range insts {
		_ = i.adapter.Stop()
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	m.mu.Lock()
	m.running = map[string]*instance{}
	m.mu.Unlock()
}

// Reload 重新读 channels.yml：先停掉现有实例，再按新配置启动。返回启动了几个。
func (m *Manager) Reload() (int, error) {
	m.Stop()
	m.mu.Lock()
	parent := m.parent
	m.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	return m.Start(parent)
}

// RunningIDs 返回正在运行的实例 ID（已排序）。
func (m *Manager) RunningIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// runInstance 把适配器跑起来；它异常返回时指数退避重连。
func (m *Manager) runInstance(ctx context.Context, inst *instance) {
	defer m.wg.Done()
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := inst.adapter.Start(ctx, func(ev channel.InboundEvent) {
			go m.handle(ctx, inst, ev)
		})
		if ctx.Err() != nil {
			return
		}
		log.Printf("[im] %s stopped: %v (retry in %s)", inst.id, err, backoff)
		if time.Since(began) > time.Minute {
			backoff = 2 * time.Second // 跑了一阵才断，重置退避
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func chatKey(inst *instance, chatID string) string { return inst.id + "|" + chatID }

func (m *Manager) chat(inst *instance, chatID string) *chatState {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := chatKey(inst, chatID)
	st := m.chats[k]
	if st == nil {
		st = &chatState{}
		m.chats[k] = st
	}
	return st
}

// SessionID 是某个会话对应的 agentgo 会话 ID；/new 之后带上代数。
func SessionID(adapterID, chatID string, gen int) string {
	if gen <= 0 {
		return "im:" + adapterID + ":" + chatID
	}
	return fmt.Sprintf("im:%s:%s#%d", adapterID, chatID, gen)
}

// ParseSessionID 反解 im 会话 ID；不是 im 会话时 ok=false。
func ParseSessionID(sessionID string) (adapterID, chatID string, ok bool) {
	rest, found := strings.CutPrefix(sessionID, "im:")
	if !found {
		return "", "", false
	}
	if i := strings.LastIndex(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	adapterID, chatID, found = strings.Cut(rest, ":")
	if !found || adapterID == "" || chatID == "" {
		return "", "", false
	}
	return adapterID, chatID, true
}

func (m *Manager) reply(inst *instance, ev channel.InboundEvent, text string) {
	replyTo := ""
	if ev.ChatType == "group" {
		replyTo = ev.MessageID
	}
	if res := inst.adapter.SendText(ev.ChatID, text, replyTo); !res.OK {
		log.Printf("[im] %s send failed: %s", inst.id, res.Error)
	}
}

// handle 处理一条入站事件（在独立 goroutine 里）。
func (m *Manager) handle(ctx context.Context, inst *instance, ev channel.InboundEvent) {
	if !inst.access.Permits(ev.UserID, ev.ChatID) {
		m.rejectOnce(inst, ev)
		return
	}
	text := strings.TrimSpace(ev.Text)
	if ev.ButtonID != "" {
		text = buttonCommand(ev.ButtonID)
	}
	if len(ev.Files) > 0 {
		var b strings.Builder
		b.WriteString(text)
		for _, f := range ev.Files {
			if f.Path != "" {
				fmt.Fprintf(&b, "\n[附件 %s: %s]", f.Type, f.Path)
			}
		}
		text = strings.TrimSpace(b.String())
	}
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		if m.command(ctx, inst, ev, text) {
			return
		}
	}
	m.run(ctx, inst, ev, text)
}

func (m *Manager) rejectOnce(inst *instance, ev channel.InboundEvent) {
	key := inst.id + "|" + ev.UserID + "|" + ev.ChatID
	m.mu.Lock()
	last := m.denied[key]
	if time.Since(last) < time.Minute {
		m.mu.Unlock()
		return
	}
	m.denied[key] = time.Now()
	m.mu.Unlock()
	m.reply(inst, ev, fmt.Sprintf(
		"未授权。管理员需要把你的 ID 加入该机器人的 allow_from：user_id=%s chat_id=%s", ev.UserID, ev.ChatID))
}

// run 把消息交给 agent，并把结果发回去。同一会话同时只跑一个。
func (m *Manager) run(ctx context.Context, inst *instance, ev channel.InboundEvent, text string) {
	st := m.chat(inst, ev.ChatID)
	st.mu.Lock()
	if st.busy {
		st.mu.Unlock()
		m.reply(inst, ev, "上一条还在处理中。发 /stop 可以中止。")
		return
	}
	st.busy = true
	runCtx, cancel := context.WithCancel(ctx)
	st.cancel = cancel
	gen := st.gen
	st.mu.Unlock()
	defer func() {
		cancel()
		st.mu.Lock()
		st.busy, st.cancel = false, nil
		st.mu.Unlock()
	}()

	sessionID := SessionID(inst.id, ev.ChatID, gen)
	_ = inst.adapter.SendTyping(ev.ChatID, ev.ContextToken)
	defer func() {
		_ = inst.adapter.StopTyping(ev.ChatID, ev.ContextToken)
		inst.adapter.Flush(ev.ChatID)
	}()

	collector := &interruptCollector{}
	res, err := m.backend.StreamChat(runCtx, gateway.ChatRequest{SessionID: sessionID, Message: text}, collector.emit)
	m.deliver(inst, ev, st, res, err, collector.take())
}

// deliver 发送结果与待批准提示。
func (m *Manager) deliver(inst *instance, ev channel.InboundEvent, st *chatState, res *gateway.ChatResult, err error, interrupts []interrupt) {
	switch {
	case err != nil && strings.Contains(err.Error(), context.Canceled.Error()):
		m.reply(inst, ev, "已中止。")
		return
	case err != nil:
		m.reply(inst, ev, "出错了："+err.Error())
		return
	}
	if res != nil {
		out := strings.TrimSpace(res.Content)
		if res.Error != "" {
			out = strings.TrimSpace(out + "\n\n出错了：" + res.Error)
		}
		if out != "" {
			m.reply(inst, ev, out)
		}
	}
	m.announceApprovals(inst, ev, st, interrupts)
}

func (m *Manager) announceApprovals(inst *instance, ev channel.InboundEvent, st *chatState, interrupts []interrupt) {
	for _, it := range interrupts {
		if it.ApprovalID == "" {
			continue
		}
		st.mu.Lock()
		st.pending = append(st.pending, it.ApprovalID)
		st.mu.Unlock()
		prompt := fmt.Sprintf("需要你批准：%s\n%s", orDefault(it.ToolName, "工具调用"), cut(it.Arguments, 400))
		if inst.adapter.SupportsButtons() {
			res := inst.adapter.SendButtons(ev.ChatID, prompt, []channel.Button{
				{ID: "approve:" + it.ApprovalID, Label: "批准"},
				{ID: "deny:" + it.ApprovalID, Label: "拒绝"},
			}, "")
			if res.OK {
				continue
			}
		}
		m.reply(inst, ev, prompt+"\n\n回复 /approve 批准，/deny 拒绝。")
	}
}

type interrupt struct {
	ApprovalID string
	ToolName   string
	Arguments  string
}

// interruptCollector 从事件流里收集“需要批准”的事件。
type interruptCollector struct {
	mu  sync.Mutex
	got []interrupt
}

func (c *interruptCollector) emit(event string, data []byte) error {
	if event != "interrupt" {
		return nil
	}
	it := parseInterrupt(data)
	c.mu.Lock()
	c.got = append(c.got, it)
	c.mu.Unlock()
	return nil
}

func (c *interruptCollector) take() []interrupt {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.got
	c.got = nil
	return out
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
