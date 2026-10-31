package im

import (
	"context"
	"encoding/json"
	"strings"

	"agentgo/internal/gateway"
	channel "agentgo/internal/imchannel"
)

const helpText = `可用命令：
/new      开始一个新会话
/stop     中止当前运行
/approve  批准待确认的操作（可带 id）
/deny     拒绝待确认的操作（可带 id）
/help     显示本帮助
其他文字会直接交给 agent。`

func parseInterrupt(data []byte) interrupt {
	var p struct {
		ApprovalID string `json:"approval_id"`
		ToolName   string `json:"tool_name"`
		Arguments  string `json:"arguments"`
	}
	_ = json.Unmarshal(data, &p)
	return interrupt{ApprovalID: p.ApprovalID, ToolName: p.ToolName, Arguments: p.Arguments}
}

// buttonCommand 把按钮回调翻译成等价的文字命令。
func buttonCommand(id string) string {
	verb, arg, ok := strings.Cut(id, ":")
	if !ok {
		return ""
	}
	switch verb {
	case "approve":
		return "/approve " + arg
	case "deny":
		return "/deny " + arg
	}
	return ""
}

// command 处理斜杠命令；返回 false 表示这不是已知命令，应当当作普通消息交给 agent。
func (m *Manager) command(ctx context.Context, inst *instance, ev channel.InboundEvent, text string) bool {
	fields := strings.Fields(text)
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	// 群里常见的 /cmd@botname 写法
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}
	st := m.chat(inst, ev.ChatID)
	switch name {
	case "help", "start":
		m.reply(inst, ev, helpText)
	case "new":
		st.mu.Lock()
		cancel := st.cancel
		st.gen++
		st.pending = nil
		st.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		m.reply(inst, ev, "已开始新会话。")
	case "stop":
		st.mu.Lock()
		cancel := st.cancel
		gen := st.gen
		st.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		stopped := m.backend.CancelSessionRun(ctx, SessionID(inst.id, ev.ChatID, gen))
		if cancel == nil && !stopped {
			m.reply(inst, ev, "当前没有在运行的任务。")
		}
	case "approve", "deny":
		m.resolve(ctx, inst, ev, st, name == "approve", arg)
	default:
		return false
	}
	return true
}

// resolve 处理 /approve /deny：不带 id 时用该会话最新的待批准项。
func (m *Manager) resolve(ctx context.Context, inst *instance, ev channel.InboundEvent, st *chatState, approve bool, id string) {
	if m.resolver == nil {
		m.reply(inst, ev, "当前后端不支持审批转发，请在桌面端处理。")
		return
	}
	st.mu.Lock()
	if id == "" && len(st.pending) > 0 {
		id = st.pending[len(st.pending)-1]
	}
	if id == "" {
		st.mu.Unlock()
		m.reply(inst, ev, "没有待批准的操作。")
		return
	}
	kept := st.pending[:0]
	for _, p := range st.pending {
		if p != id {
			kept = append(kept, p)
		}
	}
	st.pending = kept
	st.busy = true
	runCtx, cancel := context.WithCancel(ctx)
	st.cancel = cancel
	st.mu.Unlock()
	defer func() {
		cancel()
		st.mu.Lock()
		st.busy, st.cancel = false, nil
		st.mu.Unlock()
	}()

	collector := &interruptCollector{}
	res, err := m.resolver.ResolveApproval(runCtx, id, approve, collector.emit)
	if err == nil && res == nil {
		res = &gateway.ChatResult{}
	}
	m.deliver(inst, ev, st, res, err, collector.take())
}
