package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentgo/internal/sessions"
)

// Two conversations whose agents keep answering each other would burn tokens without end, so a
// sender gets only a few deliveries per window, and fewer to any one conversation.
const (
	sessionMsgWindow        = 10 * time.Minute
	sessionMsgPairLimit     = 5
	sessionMsgSenderLimit   = 20
	sessionMsgReadRunes     = 1500
	sessionMsgListScanLimit = 300
)

// rateWindow counts events per key over a sliding window. The zero value is ready to use.
type rateWindow struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

type rateRule struct {
	key   string
	limit int
}

// allow records one event under every rule's key, or none of them when a rule is already full.
func (w *rateWindow) allow(now time.Time, window time.Duration, rules ...rateRule) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hits == nil {
		w.hits = map[string][]time.Time{}
	}
	prune := func(key string) []time.Time {
		kept := w.hits[key][:0]
		for _, t := range w.hits[key] {
			if now.Sub(t) < window {
				kept = append(kept, t)
			}
		}
		w.hits[key] = kept
		return kept
	}
	for _, r := range rules {
		if len(prune(r.key)) >= r.limit {
			return false
		}
	}
	for _, r := range rules {
		w.hits[r.key] = append(w.hits[r.key], now)
	}
	return true
}

func (r *Runtime) sessionStore() (*sessions.Store, error) {
	if r.Sessions() == nil {
		return nil, errors.New("conversations are not available")
	}
	return r.Sessions(), nil
}

func (r *Runtime) ListSessions(ctx context.Context, selfID, query string, limit int) (any, error) {
	st, err := r.sessionStore()
	if err != nil {
		return nil, err
	}
	all, err := st.List(ctx, sessionMsgListScanLimit)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	runner := r.AgentRunner()
	pick := func(match func(sessions.Session) bool) []map[string]any {
		out := []map[string]any{}
		for _, s := range all {
			if s.ID == selfID || !match(s) {
				continue
			}
			out = append(out, map[string]any{
				"id": s.ID, "title": s.Title, "updated_at": s.UpdatedAt,
				"message_count": s.MessageCount, "running": runner.IsSessionRunning(s.ID),
			})
			if len(out) >= limit {
				break
			}
		}
		return out
	}
	if q == "" {
		return pick(func(sessions.Session) bool { return true }), nil
	}
	if out := pick(func(s sessions.Session) bool { return strings.Contains(strings.ToLower(s.Title), q) }); len(out) > 0 {
		return out, nil
	}
	// Titles are often generated from the first message, so a topic word rarely appears in
	// one. Measured: given an empty list the model stopped and asked the user instead of looking.
	return map[string]any{
		"note":     fmt.Sprintf("没有标题包含「%s」的会话。下面是最近的会话，标题可能是自动生成的；按内容判断，用 action=read 看看最像的几个。", strings.TrimSpace(query)),
		"sessions": pick(func(sessions.Session) bool { return true }),
	}, nil
}

func (r *Runtime) ReadSession(ctx context.Context, selfID, targetID string, limit int) (any, error) {
	st, err := r.sessionStore()
	if err != nil {
		return nil, err
	}
	sess, err := st.Get(ctx, targetID)
	if err != nil {
		return nil, fmt.Errorf("no conversation %q", targetID)
	}
	msgs, err := st.GetMessages(ctx, sess.ID, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" && m.Role != "system" {
			continue
		}
		text := m.Content
		if rs := []rune(text); len(rs) > sessionMsgReadRunes {
			text = string(rs[:sessionMsgReadRunes]) + "…（已截断）"
		}
		rows = append(rows, map[string]any{"role": m.Role, "text": text, "at": m.CreatedAt})
	}
	return map[string]any{"id": sess.ID, "title": sess.Title, "messages": rows}, nil
}

func (r *Runtime) SendSessionMessage(ctx context.Context, fromID, toID, text string) (any, error) {
	st, err := r.sessionStore()
	if err != nil {
		return nil, err
	}
	if fromID == "" {
		return nil, errors.New("this turn has no conversation to answer from")
	}
	if toID == fromID {
		return nil, errors.New("that is this conversation; write the thought in your own reply instead")
	}
	target, err := st.Get(ctx, toID)
	if err != nil {
		return nil, fmt.Errorf("no conversation %q (use action=list to find one)", toID)
	}
	runner := r.AgentRunner()
	if runner == nil {
		return nil, errors.New("agent is not ready")
	}
	if !r.sessionMsgRate.allow(time.Now(), sessionMsgWindow,
		rateRule{key: "pair:" + fromID + ">" + toID, limit: sessionMsgPairLimit},
		rateRule{key: "from:" + fromID, limit: sessionMsgSenderLimit}) {
		return nil, fmt.Errorf("too many messages sent recently (limit %d per conversation per %d minutes); wait for an answer instead of sending more",
			sessionMsgPairLimit, int(sessionMsgWindow/time.Minute))
	}
	fromTitle := fromID
	if s, err := st.Get(ctx, fromID); err == nil && strings.TrimSpace(s.Title) != "" {
		fromTitle = s.Title
	}
	runner.QueueSessionMessage(target.ID, fmt.Sprintf(
		"[来自会话「%s」(%s) 的消息]\n%s\n\n（要回复请用 session_message：action=send, to=%s）", fromTitle, fromID, text, fromID))
	return map[string]any{"delivered_to": target.ID, "title": target.Title, "target_busy": runner.IsSessionRunning(target.ID)}, nil
}
