package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// A command or experiment the agent started in the background finishes when the
// conversation has usually moved on. Without this the result sat there until
// the user happened to ask. bgWake keeps a note per finished task; once the
// session is idle it hands whatever the agent has not already read to a wake
// handler, which starts one turn in that session.
//
// A result counts as read when the agent called task_output on it and got a
// final status back: the common case is a model that starts three commands and
// then waits on each, and that turn must not be followed by a second one about
// the same results.

// BackgroundDone describes one finished background task.
type BackgroundDone struct {
	SessionID string
	Kind      string // "bash" or "job"
	ID        string
	Title     string // the command or job name
	Status    string // completed, failed, canceled
	ExitCode  int
	Output    string
}

const (
	bgWakeDebounce = 1500 * time.Millisecond // merges tasks that finish together into one turn
	bgWakeMaxWait  = 30 * time.Minute        // how long to wait for a busy session to go idle
	bgWakeTail     = 1500                    // characters of output the wake message carries
)

type bgNote struct {
	done      BackgroundDone
	raw       string // a ready-made message (the watchdog's), sent as it is
	collected bool
}

type bgWake struct {
	mu        sync.Mutex
	notes     map[string][]*bgNote // session → finished, not yet announced
	collected map[string]bool      // task IDs the agent read before they were noted
	scheduled map[string]bool
	idle      func(sessionID string) bool
	handler   func(sessionID, text string)
	debounce  time.Duration
	maxWait   time.Duration
}

func newBGWake(idle func(string) bool) *bgWake {
	return &bgWake{
		notes: map[string][]*bgNote{}, collected: map[string]bool{}, scheduled: map[string]bool{},
		idle: idle, debounce: bgWakeDebounce, maxWait: bgWakeMaxWait,
	}
}

func (w *bgWake) setHandler(fn func(sessionID, text string)) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.handler = fn
	w.mu.Unlock()
}

// done records a finished task and schedules a wake for its session. A task that
// was stopped on purpose, or that belongs to no session, wakes nobody.
func (w *bgWake) done(d BackgroundDone) {
	if w == nil || d.SessionID == "" || d.Status == "canceled" {
		return
	}
	w.mu.Lock()
	n := &bgNote{done: d, collected: d.ID != "" && w.collected[d.ID]}
	delete(w.collected, d.ID)
	w.notes[d.SessionID] = append(w.notes[d.SessionID], n)
	start := !w.scheduled[d.SessionID]
	w.scheduled[d.SessionID] = true
	w.mu.Unlock()
	if start {
		go w.waitAndWake(d.SessionID)
	}
}

// say queues a ready-made message for the session; it goes out with the next
// wake-up, once the session is idle.
func (w *bgWake) say(sessionID, text string) {
	if w == nil || sessionID == "" || text == "" {
		return
	}
	w.mu.Lock()
	w.notes[sessionID] = append(w.notes[sessionID], &bgNote{raw: text})
	start := !w.scheduled[sessionID]
	w.scheduled[sessionID] = true
	w.mu.Unlock()
	if start {
		go w.waitAndWake(sessionID)
	}
}

// collect marks a task's result as read by the agent.
func (w *bgWake) collect(id string) {
	if w == nil || id == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, notes := range w.notes {
		for _, n := range notes {
			if n.done.ID == id {
				n.collected = true
				return
			}
		}
	}
	if len(w.collected) < 256 { // read before it was noted; bounded so it cannot grow forever
		w.collected[id] = true
	}
}

func (w *bgWake) waitAndWake(session string) {
	deadline := time.Now().Add(w.maxWait)
	for {
		time.Sleep(w.debounce)
		if w.idle == nil || w.idle(session) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	w.mu.Lock()
	notes := w.notes[session]
	delete(w.notes, session)
	w.scheduled[session] = false
	handler := w.handler
	w.mu.Unlock()

	var unread []BackgroundDone
	var raw []string
	for _, n := range notes {
		switch {
		case n.raw != "":
			raw = append(raw, n.raw)
		case !n.collected:
			unread = append(unread, n.done)
		}
	}
	if (len(unread) == 0 && len(raw) == 0) || handler == nil {
		return
	}
	parts := raw
	if len(unread) > 0 {
		parts = append(parts, formatBackgroundWake(unread))
	}
	handler(session, strings.Join(parts, "\n\n"))
}

func formatBackgroundWake(done []BackgroundDone) string {
	var b strings.Builder
	b.WriteString("[后台任务完成] 你之前在后台启动的任务已经结束，结果如下，请据此继续完成之前的工作：\n")
	for _, d := range done {
		title := strings.TrimSpace(d.Title)
		if len([]rune(title)) > 120 {
			title = string([]rune(title)[:120]) + "…"
		}
		fmt.Fprintf(&b, "\n- %s %s（%s）：%s", d.Kind, d.ID, title, d.Status)
		if d.Status == "completed" || d.Status == "failed" {
			fmt.Fprintf(&b, "，退出码 %d", d.ExitCode)
		}
		if out := tailRunes(strings.TrimSpace(d.Output), bgWakeTail); out != "" {
			b.WriteString("\n  输出末尾：\n  ")
			b.WriteString(strings.ReplaceAll(out, "\n", "\n  "))
		}
	}
	return b.String()
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// bashResultSummary reads the JSON an execute_bash background task ends with.
func bashResultSummary(result string, err error) (status string, exit int, output string) {
	if err != nil {
		if strings.Contains(err.Error(), "canceled") {
			return "canceled", -1, err.Error()
		}
		return "failed", -1, err.Error()
	}
	var r struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if json.Unmarshal([]byte(result), &r) != nil {
		return "completed", 0, result
	}
	out := r.Stdout
	if strings.TrimSpace(r.Stderr) != "" {
		out += "\n" + r.Stderr
	}
	status = "completed"
	if r.ExitCode != 0 {
		status = "failed"
	}
	return status, r.ExitCode, out
}

// --- seeing which results the agent reads ---

var (
	taskOutputIDRe    = regexp.MustCompile(`"task_id"\s*:\s*"([^"]+)"`)
	taskOutputFinalRe = regexp.MustCompile(`(?mi)^Status:\s*(completed|failed|canceled|cancelled|stopped)\s*$`)
)

type bgCollectMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	w *bgWake
}

func newBGCollectMiddleware(w *bgWake) adk.ChatModelAgentMiddleware {
	if w == nil {
		return nil
	}
	return &bgCollectMiddleware{w: w}
}

func (m *bgCollectMiddleware) observe(args, result string) {
	idm := taskOutputIDRe.FindStringSubmatch(args)
	if idm == nil || !taskOutputFinalRe.MatchString(result) {
		return // still running: the agent has not got its result yet
	}
	m.w.collect(idm[1])
}

func (m *bgCollectMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tc *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	if tc == nil || tc.Name != "task_output" {
		return endpoint, nil
	}
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		out, err := endpoint(ctx, args, opts...)
		if err == nil {
			m.observe(args, out)
		}
		return out, err
	}, nil
}

func (m *bgCollectMiddleware) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, tc *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	if tc == nil || tc.Name != "task_output" {
		return endpoint, nil
	}
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, args, opts...)
		if err != nil || sr == nil {
			return sr, err
		}
		// Copy so the agent still gets the stream untouched.
		cp := sr.Copy(2)
		a, b := cp[0], cp[1]
		go func() {
			var sb strings.Builder
			for {
				chunk, rerr := b.Recv()
				if rerr != nil {
					break
				}
				sb.WriteString(chunk)
			}
			b.Close()
			m.observe(args, sb.String())
		}()
		return a, nil
	}, nil
}

// QueueSessionMessage hands text to a session as a new turn: it waits until the
// session is idle, merges with anything else queued for it, and goes out through
// the wake handler. It is how events from outside reach a conversation without
// cutting into the turn that is running.
func (r *Runner) QueueSessionMessage(sessionID, text string) {
	if r == nil {
		return
	}
	r.bgWake.say(sessionID, text)
}
