package bridge

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/crushproto"
	"agentgo/internal/engine"
)

const (
	// Crush re-sends the WHOLE message on every update, so streamed text is
	// coalesced instead of snapshotting once per token.
	crushFlushEvery = 60 * time.Millisecond
	maxToolOutput   = 30000 // runes forwarded to the TUI per tool result
)

// crushTurn turns the engine event stream of one run into Crush's message
// model: an assistant message holds text + tool calls; each tool result is its
// own role=tool message; after the last outstanding result the assistant
// message closes with finish=tool_use and the next text opens a new one.
type crushTurn struct {
	em *crushproto.Emitter

	mu       sync.Mutex
	cur      *crushproto.AssistantMsg
	open     int // tool calls awaiting a result
	buf      strings.Builder
	timer    bool
	closed   bool
	streamed bool // any assistant text arrived as deltas
	seq      int

	pend          string // possibly-partial <think> tag held back between chunks
	thinkingBuf   strings.Builder
	thinkingTimer bool
	inThinking    bool
	inThinkTag    bool

	pre   map[string]fileSnap // call id -> file content before an edit tool ran
	trees map[string]treeSnap // call id -> workspace scan before a shell command

	promptTok, complTok int

	lastID, lastText string
}

// ModelUsage implements agent.UsageObserver. Crush shows the LATEST call's
// prompt size as context usage, so later calls overwrite earlier ones.
func (t *crushTurn) ModelUsage(prompt, completion int) {
	t.mu.Lock()
	t.promptTok, t.complTok = prompt, completion
	t.mu.Unlock()
}

func (t *crushTurn) usage() (prompt, completion int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.promptTok, t.complTok
}

func newCrushTurn(em *crushproto.Emitter) *crushTurn { return &crushTurn{em: em} }

func (t *crushTurn) assistantLocked() *crushproto.AssistantMsg {
	if t.cur == nil {
		t.cur = t.em.Assistant()
	}
	return t.cur
}

func (t *crushTurn) flushThinkingLocked() {
	t.thinkingTimer = false
	if t.closed || t.thinkingBuf.Len() == 0 {
		return
	}
	t.assistantLocked().AppendReasoning(t.thinkingBuf.String())
	t.thinkingBuf.Reset()
}

func (t *crushTurn) stopThinkingLocked() {
	if t.inThinking {
		t.flushThinkingLocked()
		if t.cur != nil {
			t.cur.ReasoningDone()
		}
		t.inThinking = false
	}
}

func (t *crushTurn) flushLocked() {
	t.timer = false
	if t.closed || t.buf.Len() == 0 {
		return
	}
	t.stopThinkingLocked()
	t.assistantLocked().AppendText(t.buf.String())
	t.buf.Reset()
}

// onEvent is the engine.EventSink.
func (t *crushTurn) onEvent(e engine.Event) {
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	if e.Type != engine.EventToken && e.Type != engine.EventReasoning { // deltas would flood the log
		agent.DebugLogf("engine event: type=%v name=%q id=%q err=%v out=%.120q", e.Type, str("name"), str("id"), e.Payload["is_error"], str("output"))
	}
	switch e.Type {
	case engine.EventReasoning:
		t.reasoning(str("delta"))
	case engine.EventToken:
		t.token(str("delta"))
	case engine.EventToolCall:
		t.toolCall(str("id"), str("name"), str("arguments"))
	case engine.EventFileChange:
		t.em.FileChanged(str("path"), str("before"), str("after"))
	case engine.EventToolResult:
		isErr, _ := e.Payload["is_error"].(bool)
		t.toolResult(str("id"), str("name"), str("output"), isErr)
	}
}

func (t *crushTurn) reasoning(delta string) {
	if delta == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.reasoningLocked(delta)
}

func (t *crushTurn) reasoningLocked(delta string) {
	if delta == "" || t.closed {
		return
	}
	a := t.assistantLocked()
	if !t.inThinking {
		t.inThinking = true
		a.ReasoningStart()
	}
	t.thinkingBuf.WriteString(delta)
	if !t.thinkingTimer {
		t.thinkingTimer = true
		time.AfterFunc(crushFlushEvery, func() {
			t.mu.Lock()
			t.flushThinkingLocked()
			t.mu.Unlock()
		})
	}
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// token consumes one streamed text delta. Models that reason inline wrap the
// thinking in <think>…</think>; a tag can be split across chunks ("</th"+"ink>"),
// so a possibly-partial tag at the end of the data is held back in t.pend until
// the next chunk (or the end of the segment) decides what it was.
func (t *crushTurn) token(delta string) {
	if delta == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.pend += delta
	for {
		tag := thinkOpen
		if t.inThinkTag {
			tag = thinkClose
		}
		if i := strings.Index(t.pend, tag); i >= 0 {
			t.emitLocked(t.pend[:i])
			t.pend = t.pend[i+len(tag):]
			if t.inThinkTag {
				t.inThinkTag = false
				t.stopThinkingLocked()
			} else {
				t.inThinkTag = true
			}
			continue
		}
		keep := partialTagSuffix(t.pend, tag)
		t.emitLocked(t.pend[:len(t.pend)-keep])
		t.pend = t.pend[len(t.pend)-keep:]
		return
	}
}

// emitLocked routes already-classified text to the reasoning or text part.
func (t *crushTurn) emitLocked(s string) {
	if s == "" {
		return
	}
	if t.inThinkTag {
		t.reasoningLocked(s)
	} else {
		t.tokenLocked(s)
	}
}

// flushPendLocked releases a held-back partial tag as ordinary content (it
// turned out not to be a tag: the segment ended). Call before closing a segment.
func (t *crushTurn) flushPendLocked() {
	if t.pend == "" {
		return
	}
	s := t.pend
	t.pend = ""
	t.emitLocked(s)
}

// partialTagSuffix is the length of the longest proper prefix of tag that s ends with.
func partialTagSuffix(s, tag string) int {
	for k := min(len(tag)-1, len(s)); k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
func (t *crushTurn) tokenLocked(delta string) {
	if delta == "" || t.closed {
		return
	}
	t.stopThinkingLocked()
	t.streamed = true
	t.buf.WriteString(delta)
	if !t.timer {
		t.timer = true
		time.AfterFunc(crushFlushEvery, func() {
			t.mu.Lock()
			t.flushLocked()
			t.mu.Unlock()
		})
	}
}

func (t *crushTurn) callID(id string) string {
	if id != "" {
		return id
	}
	t.seq++
	return "call_agentgo_" + strconv.Itoa(t.seq)
}

func (t *crushTurn) toolCall(id, name, args string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return id
	}
	t.flushPendLocked()
	t.stopThinkingLocked()
	t.flushLocked()
	id = t.callID(id)
	if snap, ok := snapshotBefore(t.em.WorkDir(), name, args); ok && snap.ok {
		if t.pre == nil {
			t.pre = map[string]fileSnap{}
		}
		t.pre[id] = snap
	}
	if name == shellTool {
		if t.trees == nil {
			t.trees = map[string]treeSnap{}
		}
		t.trees[id] = scanTree(t.em.WorkDir())
	}
	a := t.assistantLocked()
	a.ToolCallStart(id, name)
	a.ToolCallInput(id, args)
	t.open++
	return id
}

func (t *crushTurn) toolResult(id, name, output string, isErr bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if r := []rune(output); len(r) > maxToolOutput {
		output = string(r[:maxToolOutput]) + "\n… (truncated)"
	}
	if snap, ok := t.pre[id]; ok {
		delete(t.pre, id)
		if !isErr {
			if after, ok := readSnapshot(snap.path); ok {
				t.em.FileChanged(snap.path, snap.before, after)
			}
		}
	}
	if before, ok := t.trees[id]; ok {
		delete(t.trees, id)
		for _, d := range diffTrees(before, scanTree(t.em.WorkDir())) {
			t.em.FileChanged(d.path, d.before, d.after)
		}
	}
	t.em.ToolResult(id, name, output, "", isErr)
	if t.open--; t.open <= 0 {
		t.open = 0
		t.closeLocked("tool_use")
	}
}

// text appends final text (used for errors and resumed-after-approval output).
func (t *crushTurn) text(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.flushPendLocked()
	t.stopThinkingLocked()
	t.streamed = true
	t.flushLocked()
	t.assistantLocked().AppendText(s)
}

func (t *crushTurn) closeLocked(reason string) {
	t.flushPendLocked()
	t.stopThinkingLocked()
	t.flushLocked()
	if t.cur != nil {
		t.cur.Finish(reason)
		t.lastID, t.lastText = t.cur.ID(), t.cur.Text()
		t.cur = nil
	}
}

// finish closes the turn; later deltas are ignored.
func (t *crushTurn) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeLocked("end_turn")
	t.closed = true
}

func (t *crushTurn) sawText() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.streamed
}
