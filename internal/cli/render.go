package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"agentgo/internal/engine"
)

// Renderer turns engine events into terminal output.
//
// Text mode keeps stdout for the assistant's answer only, so `agentgo-cli run`
// pipes cleanly; the running commentary (tool calls, results, file changes,
// reasoning) goes to stderr. JSON mode writes one JSON object per event to
// stdout instead and nothing else.
type Renderer struct {
	Out, Err io.Writer
	JSON     bool
	Thinking bool // show reasoning deltas (text mode)
	Color    bool

	mu       sync.Mutex
	streamed bool // assistant tokens were written this turn
	lineOpen bool // stdout is mid-line
	errOpen  bool // stderr is mid-line (reasoning stream)
}

// Emit implements engine.EventSink.
func (r *Renderer) Emit(e engine.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.JSON {
		if d, _ := e.Payload["delta"].(string); e.Type == engine.EventToken && d != "" {
			r.streamed = true // the final message would repeat what the token events carried
		}
		r.jsonLine(e)
		return
	}
	switch e.Type {
	case engine.EventToken:
		d, _ := e.Payload["delta"].(string)
		r.closeErr()
		r.write(r.Out, d)
		if d != "" {
			r.streamed = true
			r.lineOpen = !strings.HasSuffix(d, "\n")
		}
	case engine.EventReasoning:
		if !r.Thinking {
			return
		}
		d, _ := e.Payload["delta"].(string)
		r.endLine()
		r.write(r.Err, r.dim(d))
		if d != "" {
			r.errOpen = !strings.HasSuffix(d, "\n")
		}
	case engine.EventToolCall:
		r.endLine()
		name, _ := e.Payload["name"].(string)
		args, _ := e.Payload["arguments"].(string)
		r.line(r.Err, r.dim("▸ "+name+" "+oneLine(args, 140)))
	case engine.EventToolResult:
		r.endLine()
		out, _ := e.Payload["output"].(string)
		isErr, _ := e.Payload["is_error"].(bool)
		mark := "✓"
		if isErr {
			mark = "✗"
		}
		r.line(r.Err, r.dim("  "+mark+" "+oneLine(out, 140)))
	case engine.EventFileChange:
		r.endLine()
		p, _ := e.Payload["path"].(string)
		r.line(r.Err, r.dim("  ± "+p))
	case engine.EventError:
		r.endLine()
		m, _ := e.Payload["message"].(string)
		r.line(r.Err, "error: "+m)
	}
}

// Message prints one finished assistant message (a resumed run's output, or a
// run's final text when nothing streamed).
func (r *Renderer) Message(m Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.JSON {
		r.jsonValue(map[string]any{"type": "message", "role": m.Role, "kind": m.Type, "content": m.Content})
		return
	}
	if m.Content == "" {
		return
	}
	r.endLine()
	r.write(r.Out, m.Content)
	r.lineOpen = !strings.HasSuffix(m.Content, "\n")
	r.streamed = true
}

// Notice prints a status line for the human (never to stdout in text mode).
func (r *Renderer) Notice(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.JSON {
		r.jsonValue(map[string]any{"type": "notice", "text": s})
		return
	}
	r.endLine()
	r.line(r.Err, s)
}

// Streamed reports whether the assistant's text already reached stdout this turn.
func (r *Renderer) Streamed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.streamed
}

// BeginTurn resets per-turn state.
func (r *Renderer) BeginTurn() {
	r.mu.Lock()
	r.streamed = false
	r.mu.Unlock()
}

// EndTurn terminates an unfinished output line.
func (r *Renderer) EndTurn() {
	r.mu.Lock()
	r.endLine()
	r.mu.Unlock()
}

func (r *Renderer) endLine() {
	r.closeErr()
	if r.lineOpen && !r.JSON {
		r.write(r.Out, "\n")
		r.lineOpen = false
	}
}

func (r *Renderer) closeErr() {
	if r.errOpen {
		r.write(r.Err, "\n")
		r.errOpen = false
	}
}

func (r *Renderer) line(w io.Writer, s string) { r.write(w, s+"\n") }

func (r *Renderer) write(w io.Writer, s string) {
	if w != nil && s != "" {
		_, _ = io.WriteString(w, s)
	}
}

func (r *Renderer) dim(s string) string {
	if !r.Color || s == "" {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

func (r *Renderer) jsonLine(e engine.Event) { r.jsonValue(e) }

func (r *Renderer) jsonValue(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(r.Err, "render: %v\n", err)
		return
	}
	r.write(r.Out, string(b)+"\n")
}

// oneLine collapses s to a single line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) > n {
		return string(rs[:n]) + "…"
	}
	return s
}
