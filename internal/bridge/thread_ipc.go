package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentgo/internal/gateway"
	"agentgo/internal/threadsub"
)

const maxThreadEventChars = 20000

// --- tools.ThreadHandler: the agent follows a thread from its own session ---

func (r *Runtime) threadStore() (*threadsub.Store, error) {
	if r.threads == nil {
		return nil, errors.New("thread subscriptions are not available")
	}
	return r.threads, nil
}

func (r *Runtime) SubscribeThread(_ context.Context, sessionID, key, title string) (any, error) {
	st, err := r.threadStore()
	if err != nil {
		return nil, err
	}
	if _, err := st.Bind(key, sessionID, title); err != nil {
		return nil, err
	}
	nk, _ := threadsub.NormalizeKey(key)
	sub, _ := st.Resolve(nk)
	return sub, nil
}

func (r *Runtime) UnsubscribeThread(_ context.Context, sessionID, key string) error {
	st, err := r.threadStore()
	if err != nil {
		return err
	}
	return st.Unbind(key, sessionID)
}

func (r *Runtime) ListThreads(_ context.Context, sessionID string) (any, error) {
	st, err := r.threadStore()
	if err != nil {
		return nil, err
	}
	return st.List(sessionID), nil
}

// DeliverThreadEvent sends an outside event to the session that follows its
// thread. With create set, a thread nobody follows yet gets a session of its
// own, derived from the key, so the next event for it lands in the same place.
// The event becomes a turn once the session is idle; it never cuts into one.
func (r *Runtime) DeliverThreadEvent(ctx context.Context, key, source, text string, create bool) (string, error) {
	st, err := r.threadStore()
	if err != nil {
		return "", err
	}
	key, err = threadsub.NormalizeKey(key)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("event text is required")
	}
	if rs := []rune(text); len(rs) > maxThreadEventChars {
		text = string(rs[:maxThreadEventChars]) + "\n…（已截断）"
	}
	runner := r.AgentRunner()
	if runner == nil {
		return "", errors.New("agent is not ready")
	}
	sub, ok := st.Resolve(key)
	if !ok {
		if !create {
			return "", fmt.Errorf("%w: %s", gateway.ErrThreadNotFound, key)
		}
		sid := threadsub.SessionFor(key)
		if _, err := st.Bind(key, sid, ""); err != nil {
			return "", err
		}
		sub, _ = st.Resolve(key)
	}
	if r.Sessions() != nil {
		if err := r.Sessions().EnsureSession(ctx, sub.SessionID); err != nil {
			return "", err
		}
	}
	src := strings.TrimSpace(source)
	if src == "" {
		src = "external"
	}
	runner.QueueSessionMessage(sub.SessionID, fmt.Sprintf("[线程事件 %s · %s]\n%s", key, src, text))
	st.Delivered(key)
	return sub.SessionID, nil
}

// --- gateway.ThreadBackend ---

func (g *RuntimeGateway) DeliverThread(ctx context.Context, key, source, text string, create bool) (string, error) {
	return g.rt.DeliverThreadEvent(ctx, key, source, text, create)
}

func (g *RuntimeGateway) ListThreads(ctx context.Context) ([]byte, error) {
	v, err := g.rt.ListThreads(ctx, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func (g *RuntimeGateway) BindThread(ctx context.Context, key, sessionID, title string) error {
	st, err := g.rt.threadStore()
	if err != nil {
		return err
	}
	if g.rt.Sessions() != nil {
		if err := g.rt.Sessions().EnsureSession(ctx, sessionID); err != nil {
			return err
		}
	}
	_, err = st.Bind(key, sessionID, title)
	return err
}

func (g *RuntimeGateway) UnbindThread(_ context.Context, key string) error {
	st, err := g.rt.threadStore()
	if err != nil {
		return err
	}
	return st.Unbind(key, "")
}
