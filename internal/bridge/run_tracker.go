package bridge

import (
	"strings"
	"sync"
	"time"

	"agentgo/internal/applog"
)

// RunTracker records in-flight agent runs for UI diagnostics (GetAgentRunStatus).
type RunTracker struct {
	mu   sync.RWMutex
	runs map[string]*runSnap
}

type runSnap struct {
	SessionID  string
	Phase      string
	Detail     string
	StartedAt  time.Time
	UpdatedAt  time.Time
	StreamAck  bool
	ChunkCount int
	TraceHint  string
	LastError  string
}

func NewRunTracker() *RunTracker {
	return &RunTracker{runs: make(map[string]*runSnap)}
}

func (t *RunTracker) Begin(sessionID, detail string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.runs[sessionID] = &runSnap{
		SessionID: sessionID, Phase: "ipc_ack", Detail: detail,
		StartedAt: now, UpdatedAt: now, StreamAck: true,
	}
	t.mu.Unlock()
	applog.UI("run begin session=%s detail=%q", sessionID, detail)
}

func (t *RunTracker) Set(sessionID, phase, detail string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	r, ok := t.runs[sessionID]
	if !ok {
		r = &runSnap{SessionID: sessionID, StartedAt: time.Now(), StreamAck: true}
		t.runs[sessionID] = r
	}
	r.Phase = phase
	r.Detail = detail
	r.UpdatedAt = time.Now()
	t.mu.Unlock()
	applog.UI("run session=%s phase=%s detail=%q", sessionID, phase, detail)
}

func (t *RunTracker) AddChunk(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if r, ok := t.runs[sessionID]; ok {
		r.ChunkCount++
		r.UpdatedAt = time.Now()
		if r.Phase == "ipc_ack" || r.Phase == "goroutine" || r.Phase == "agent_run" {
			r.Phase = "streaming"
		}
	}
	t.mu.Unlock()
}

func (t *RunTracker) SetTrace(sessionID, component, name string) {
	hint := strings.TrimSpace(component + "/" + name)
	if hint == "/" {
		return
	}
	t.mu.Lock()
	if r, ok := t.runs[sessionID]; ok {
		r.TraceHint = hint
		r.UpdatedAt = time.Now()
	}
	t.mu.Unlock()
}

func (t *RunTracker) Finish(sessionID, phase, errMsg string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if r, ok := t.runs[sessionID]; ok {
		r.Phase = phase
		r.LastError = errMsg
		r.UpdatedAt = time.Now()
	}
	t.mu.Unlock()
	applog.UI("run finish session=%s phase=%s err=%q", sessionID, phase, errMsg)
}

func (t *RunTracker) Clear(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	t.mu.Lock()
	delete(t.runs, sessionID)
	t.mu.Unlock()
}

func (t *RunTracker) Snapshot(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	t.mu.RLock()
	r, ok := t.runs[sessionID]
	if !ok {
		t.mu.RUnlock()
		return map[string]any{
			"session_id": sessionID,
			"phase":      "idle",
			"hint":       "后端无进行中的任务（可能 IPC 未到达或已完成）",
		}
	}
	elapsed := int(time.Since(r.StartedAt).Seconds())
	idle := int(time.Since(r.UpdatedAt).Seconds())
	out := map[string]any{
		"session_id":  r.SessionID,
		"phase":       r.Phase,
		"detail":      r.Detail,
		"elapsed_sec": elapsed,
		"idle_sec":    idle,
		"stream_ack":  r.StreamAck,
		"chunk_count": r.ChunkCount,
		"trace":       r.TraceHint,
		"error":       r.LastError,
	}
	t.mu.RUnlock()
	out["hint"] = runHint(out)
	return out
}

func runHint(s map[string]any) string {
	phase, _ := s["phase"].(string)
	chunks, _ := s["chunk_count"].(int)
	trace, _ := s["trace"].(string)
	errMsg, _ := s["error"].(string)
	if errMsg != "" {
		return "错误: " + errMsg
	}
	switch phase {
	case "idle":
		return "后端空闲：若界面仍在转圈，多半是前端未收到事件或旧进程"
	case "ipc_ack":
		return "已收到 SendMessageStream，等待 goroutine 启动"
	case "goroutine", "agent_run":
		if trace != "" {
			return "Agent 运行中 · " + trace + "（等待 API/工具返回）"
		}
		return "Agent 运行中（可能在调 LLM API，首字可能很慢）"
	case "streaming":
		return "正在流式输出"
	case "done":
		return "后端已完成，若界面无响应则是事件未送达"
	case "cancelled":
		return "已取消"
	case "error":
		return "运行失败，见 error 字段"
	default:
		if chunks == 0 && (phase == "agent_run" || phase == "goroutine") {
			return "长时间无 chunk：高概率卡在 LLM API（超时 120s 后会报错）"
		}
		return "phase=" + phase
	}
}
